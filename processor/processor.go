package processor

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"newsletterdigest_go/config"
	"newsletterdigest_go/fetcher"
	"newsletterdigest_go/models"
	"newsletterdigest_go/openai"
	"newsletterdigest_go/utils"
	"newsletterdigest_go/validator"
)

const Separator = "\n\n——————————————————\n\n"

type Processor struct {
	openaiClient   *openai.Client
	config         *config.Config
	contentFetcher *fetcher.ContentFetcher
}

func New(client *openai.Client, cfg *config.Config) *Processor {
	return &Processor{
		openaiClient:   client,
		config:         cfg,
		contentFetcher: fetcher.New(),
	}
}

func (p *Processor) ProcessNewsletters(ctx context.Context, newsletters []*models.Newsletter) (string, []*models.Newsletter, error) {
	var perSummaries []string
	var processedItems []*models.Newsletter

	// Process newsletters if available
	if len(newsletters) > 0 {
		fmt.Printf("Processing %d newsletters...\n", len(newsletters))
		for _, newsletter := range newsletters {
			summary, err := p.summarizeSingle(ctx, newsletter)
			if err != nil {
				continue
			}

			perSummaries = append(perSummaries, fmt.Sprintf("### %s\n%s\nLinks:\n%s",
				newsletter.Subject, summary, strings.Join(newsletter.Links, "\n")))
			processedItems = append(processedItems, newsletter)

			time.Sleep(p.config.PerEmailSleep)
		}
	} else {
		fmt.Println("No newsletters found")
	}

	// Fetch LinkedIn content if enabled
	var linkedInSummaries []string
	shouldFetchLinkedIn := p.config.FetchLinkedInHashtags && len(p.config.LinkedInHashtags) > 0 &&
		(len(newsletters) > 0 || p.config.LinkedInOnlyMode)

	if shouldFetchLinkedIn {
		if len(newsletters) == 0 {
			fmt.Println("LinkedIn-only mode: fetching LinkedIn content without newsletters")
		}

		linkedInContent, err := p.fetchLinkedInContent(ctx)
		if err != nil {
			fmt.Printf("Error fetching LinkedIn content: %v\n", err)
		} else {
			linkedInSummaries = linkedInContent
		}
	}

	// Combine newsletter and LinkedIn summaries
	allSummaries := append(perSummaries, linkedInSummaries...)

	if len(allSummaries) == 0 {
		if p.config.LinkedInOnlyMode && p.config.FetchLinkedInHashtags {
			return "", nil, fmt.Errorf("no content found: no newsletters and LinkedIn fetching failed")
		}
		return "", nil, fmt.Errorf("no usable content extracted")
	}

	// Determine digest type for email subject
	digestType := p.determineDigestType(len(perSummaries), len(linkedInSummaries))

	finalHTML, err := p.synthesizeFinal(ctx, allSummaries, processedItems, digestType)
	if err != nil {
		// fallback to raw bullets
		return p.createFallbackHTML(allSummaries, digestType), processedItems, nil
	}

	// Add verification footer and optional sample
	if v := validator.ValidateOutput(finalHTML); v != "" {
		log.Printf("validator: %s", v)
	}

	// Insert footer before closing body tag (we know the structure now)
	if p.config.ShowFooter {
		footerHTML := "\n  <div class=\"footer\">\n" + p.createVerificationFooter(processedItems, len(linkedInSummaries))
		if p.config.AppendSample {
			footerHTML += p.appendPerEmailSample("", allSummaries)
		}
		footerHTML += "\n  </div>\n"

		// Insert before </body>
		finalHTML = strings.Replace(finalHTML, "</body>", footerHTML+"</body>", 1)
	}

	return finalHTML, processedItems, nil
}

func (p *Processor) determineDigestType(newsletterCount, linkedInCount int) string {
	if newsletterCount > 0 && linkedInCount > 0 {
		return "Combined"
	} else if newsletterCount > 0 {
		return "Newsletter"
	} else if linkedInCount > 0 {
		return "LinkedIn"
	}
	return "Empty"
}

func (p *Processor) summarizeSingle(ctx context.Context, newsletter *models.Newsletter) (string, error) {
	body := utils.CleanText(newsletter.Text)
	if len(body) == 0 {
		return "", fmt.Errorf("empty body")
	}

	// Check if we should fetch additional content (e.g., LinkedIn articles)
	if p.config.FetchFullContent {
		if shouldFetch, url := p.contentFetcher.ShouldFetchContent(body, newsletter.Links); shouldFetch {
			fmt.Printf("Detected LinkedIn newsletter teaser for: %s\n", newsletter.Subject)
			if fullContent, err := p.contentFetcher.FetchLinkedInContent(url); err == nil {
				fmt.Printf("Successfully fetched additional content (%d chars)\n", len(fullContent))
				// Combine the teaser with the full content
				body = body + "\n\n--- Full Article Content ---\n" + fullContent
			} else {
				fmt.Printf("Failed to fetch content: %v\n", err)
				// Continue with just the teaser content
			}
		}
	}

	if len(body) > p.config.PerEmailMaxChars {
		body = body[:p.config.PerEmailMaxChars]
	}

	var lb strings.Builder
	if len(newsletter.Links) > 0 {
		lb.WriteString("Relevant links:\n")
		for i := range newsletter.Links {
			lb.WriteString(fmt.Sprintf("- %s\n", newsletter.Links[i]))
		}
		lb.WriteString("\n")
	}

	user := fmt.Sprintf(
		"Subject: %s\nFrom: %s\nDate: %s\nSummarize this single newsletter into 3–6 crisp bullets. "+
			"Emphasize Product Management, Healthcare, Architecture, Team Organization. "+
			"Only include AI if it clearly impacts those. If a bullet references something with a URL, keep a short cue like [L1], [L2] inline (no HTML/Markdown), referring to the 'Relevant links' list.\n\n%s%s",
		newsletter.Subject, newsletter.From, newsletter.Date, lb.String(), body,
	)

	messages := []openai.ChatMessage{
		{Role: "system", Content: p.config.PromptSingle},
		{Role: "user", Content: user},
	}

	return p.openaiClient.Chat(ctx, p.config.SmallModel, messages, 0.1, 300)
}

func (p *Processor) fetchLinkedInContent(ctx context.Context) ([]string, error) {
	fmt.Printf("Fetching LinkedIn content for hashtags: %v\n", p.config.LinkedInHashtags)

	// Fetch LinkedIn posts for the configured hashtags
	posts, err := p.contentFetcher.FetchLinkedInHashtagContent(p.config.LinkedInHashtags, 15, p.config.LinkedInFetchFullContent) // Fetch more to account for filtering
	if err != nil {
		return nil, err
	}

	if len(posts) == 0 {
		fmt.Println("No LinkedIn posts found for the specified hashtags")
		return nil, nil
	}

	fmt.Printf("Found %d LinkedIn posts, filtering for professional content...\n", len(posts))

	// Filter out promotional/advertising content if enabled
	var filteredPosts []fetcher.LinkedInPost
	if p.config.LinkedInFilterPromotional {
		for _, post := range posts {
			if p.isContentProfessional(ctx, post) {
				filteredPosts = append(filteredPosts, post)
			} else {
				fmt.Printf("Filtered out promotional post by %s\n", post.Author)
			}

			// Limit to target number after filtering
			if len(filteredPosts) >= 10 {
				break
			}
		}
		fmt.Printf("After filtering: %d professional posts remain\n", len(filteredPosts))
	} else {
		// No filtering, use all posts but limit to 10
		filteredPosts = posts
		if len(filteredPosts) > 10 {
			filteredPosts = filteredPosts[:10]
		}
		fmt.Printf("Content filtering disabled, using %d posts\n", len(filteredPosts))
	}

	if len(filteredPosts) == 0 {
		fmt.Println("No professional content found after filtering")
		return nil, nil
	}

	// Summarize each professional LinkedIn post
	var summaries []string
	for _, post := range filteredPosts {
		summary, err := p.summarizeLinkedInPost(ctx, post)
		if err != nil {
			fmt.Printf("Error summarizing LinkedIn post: %v\n", err)
			continue
		}

		summaries = append(summaries, fmt.Sprintf("### LinkedIn: %s\n%s\nSource: %s",
			post.Author, summary, post.URL))
	}

	return summaries, nil
}

func (p *Processor) isContentProfessional(ctx context.Context, post fetcher.LinkedInPost) bool {
	// Quick heuristics first (fast filtering)
	if p.hasPromotionalIndicators(post.Text) {
		return false
	}

	// Use AI for more sophisticated content analysis
	return p.aiContentFilter(ctx, post)
}

func (p *Processor) hasPromotionalIndicators(text string) bool {
	textLower := strings.ToLower(text)

	// Strong promotional indicators
	strongIndicators := []string{
		"buy now", "purchase", "sale", "discount", "% off", "limited time",
		"click here", "link in bio", "dm me", "contact me for pricing",
		"special offer", "free trial", "get started today", "sign up now",
		"use code", "promo code", "exclusive deal", "order now",
		"available for purchase", "book a call", "schedule a demo",
		"starting at $", "price", "cost", "investment",
	}

	for _, indicator := range strongIndicators {
		if strings.Contains(textLower, indicator) {
			return true // Definitely promotional
		}
	}

	// Weak indicators (need AI confirmation)
	weakIndicators := []string{
		"solution", "service", "product", "offer", "help you",
		"we provide", "our company", "my company", "new launch",
		"announcement", "introducing", "check out", "learn more",
	}

	count := 0
	for _, indicator := range weakIndicators {
		if strings.Contains(textLower, indicator) {
			count++
		}
	}

	// If too many weak indicators, likely promotional
	return count >= 3
}

func (p *Processor) aiContentFilter(ctx context.Context, post fetcher.LinkedInPost) bool {
	// Use AI to determine if content is professional insight vs. promotional
	prompt := fmt.Sprintf(
		"Analyze this LinkedIn post and determine if it contains valuable professional insights or if it's primarily promotional/advertising content.\n\n"+
			"Post by: %s\n"+
			"Content: %s\n\n"+
			"Respond with exactly 'PROFESSIONAL' if the post contains:\n"+
			"- Industry insights, trends, or analysis\n"+
			"- Professional advice or best practices\n"+
			"- Thought leadership or expert opinions\n"+
			"- Educational content or case studies\n"+
			"- News or developments in the field\n\n"+
			"Respond with exactly 'PROMOTIONAL' if the post contains:\n"+
			"- Product advertisements or sales pitches\n"+
			"- Service promotions or marketing\n"+
			"- Direct selling or lead generation\n"+
			"- Event promotion (unless highly educational)\n"+
			"- Generic company announcements\n\n"+
			"Focus on the primary intent and value of the content.",
		post.Author, post.Text)

	messages := []openai.ChatMessage{
		{Role: "system", Content: "You are a content quality filter for professional insights. Respond only with 'PROFESSIONAL' or 'PROMOTIONAL'."},
		{Role: "user", Content: prompt},
	}

	response, err := p.openaiClient.Chat(ctx, p.config.SmallModel, messages, 0.1, 50)
	if err != nil {
		fmt.Printf("AI filter error for post by %s: %v\n", post.Author, err)
		// If AI fails, fall back to heuristics - err on side of inclusion for professional-looking content
		return !p.hasStrongPromotionalLanguage(post.Text)
	}

	response = strings.ToUpper(strings.TrimSpace(response))
	isProfessional := response == "PROFESSIONAL"

	if !isProfessional {
		fmt.Printf("AI classified as promotional: %s (by %s)\n",
			p.truncateText(post.Text, 100), post.Author)
	}

	return isProfessional
}

func (p *Processor) hasStrongPromotionalLanguage(text string) bool {
	textLower := strings.ToLower(text)
	strongPromotional := []string{
		"buy now", "purchase", "sale", "discount", "% off",
		"click here", "dm me", "contact me", "book a call",
		"special offer", "limited time", "promo code",
	}

	for _, indicator := range strongPromotional {
		if strings.Contains(textLower, indicator) {
			return true
		}
	}
	return false
}

func (p *Processor) truncateText(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength] + "..."
}

func (p *Processor) summarizeLinkedInPost(ctx context.Context, post fetcher.LinkedInPost) (string, error) {
	user := fmt.Sprintf(
		"Author: %s\nHashtags: %s\nTimestamp: %s\nSummarize this LinkedIn post into 2-4 crisp bullets. "+
			"Focus on insights relevant to Product Management, Healthcare, Architecture, Team Organization, or AI. "+
			"Extract key takeaways and actionable insights. Keep it concise and professional.\n\nPost Content:\n%s",
		post.Author, strings.Join(post.Hashtags, ", "), post.Timestamp, post.Text,
	)

	messages := []openai.ChatMessage{
		{Role: "system", Content: "You summarize LinkedIn posts for a product executive. Focus on actionable insights and key trends. Return 2-4 short bullets as plain text lines (no HTML/Markdown)."},
		{Role: "user", Content: user},
	}

	return p.openaiClient.Chat(ctx, p.config.SmallModel, messages, 0.1, 200)
}

func (p *Processor) synthesizeFinal(ctx context.Context, perSumm []string, meta []*models.Newsletter, digestType string) (string, error) {
	// Build link index with global numbering
	var li []string
	linkCounter := 1
	for _, m := range meta {
		if len(m.Links) == 0 {
			continue
		}
		var lines []string
		for _, u := range m.Links {
			lines = append(lines, fmt.Sprintf("[L%d] %s", linkCounter, u))
			linkCounter++
		}
		li = append(li, fmt.Sprintf("%s\n%s", m.Subject, strings.Join(lines, "\n")))
	}
	linkIndex := strings.Join(li, "\n\n")
	joined := strings.Join(perSumm, Separator)

	system := p.config.PromptFinal
	user := "Combine and rank the following content into a weekly digest, grouped in this EXACT order:\n" +
		"1) === Product Management ===\n" +
		"2) === Healthcare ===\n" +
		"3) === Software Architecture ===\n" +
		"4) === Team Organization ===\n" +
		"5) === AI ===\n" +
		"Content includes both newsletter summaries and LinkedIn insights. Merge related content into appropriate sections.\n" +
		"IMPORTANT: For Software Architecture section - ONLY include content about software architecture, system design, cloud architecture, microservices, APIs, technical architecture, DevOps, infrastructure. DO NOT include building architecture, physical architecture, or construction-related content.\n" +
		"Rules:\n" +
		"- MUST generate ALL 5 sections above in exact order, even if brief\n" +
		"- Follow each section header with bullet points starting with '- '\n" +
		"- If no content for a section, add '- No significant updates this week'\n" +
		"- Merge similar content from newsletters and LinkedIn posts\n" +
		"- Preserve key facts/metrics, include short source names\n" +
		"- Keep [L1], [L2] references as-is in the text for link replacement\n" +
		"- No HTML, Markdown, or special formatting - just plain text\n\n" +
		"=== CONTENT TO PROCESS ===\n" + joined +
		"\n\n=== LINK INDEX (map [L*] to URLs) ===\n" + linkIndex + "\n\n" +
		"Generate exactly 5 sections as specified above, combining newsletter and LinkedIn insights."

	messages := []openai.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	out, err := p.openaiClient.Chat(ctx, p.config.FinalModel, messages, 0.1, 2000)
	if err != nil {
		return "", err
	}

	fmt.Printf("Raw Claude plain text length: %d chars\n", len(out))
	fmt.Printf("Raw Claude plain text (first 800 chars): %s...\n", out[:min(800, len(out))])

	// Show what sections were detected
	sections := p.detectSections(out)
	fmt.Printf("Detected sections: %v\n", sections)

	// Parse the plain text and convert to HTML
	htmlContent := p.parseTextToHTML(strings.TrimSpace(out), meta)

	// Build the complete HTML document ourselves
	finalHTML := p.buildCompleteHTML(htmlContent, digestType)

	fmt.Printf("Final HTML length: %d chars\n", len(finalHTML))

	return finalHTML, nil
}

func (p *Processor) parseTextToHTML(text string, meta []*models.Newsletter) string {
	var html strings.Builder

	// Build link map for [L1], [L2] replacement with newsletter metadata
	type sourceInfo struct {
		subject string
		date    string
	}
	linkMap := make(map[string]sourceInfo)
	linkCounter := 1
	for _, m := range meta {
		for range m.Links {
			linkKey := fmt.Sprintf("[L%d]", linkCounter)
			linkMap[linkKey] = sourceInfo{
				subject: m.Subject,
				date:    m.Date,
			}
			linkCounter++
		}
	}

	lines := strings.Split(text, "\n")
	inSection := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for section headers like "=== Product Management ==="
		if strings.HasPrefix(line, "===") && strings.HasSuffix(line, "===") {
			// Close previous section if open
			if inSection {
				html.WriteString("  </ul>\n")
			}

			// Extract section name
			sectionName := strings.TrimSpace(strings.Trim(line, "="))
			html.WriteString(fmt.Sprintf("  <h2>%s</h2>\n", utils.HtmlEscape(sectionName)))
			html.WriteString("  <ul>\n")
			inSection = true
		} else if strings.HasPrefix(line, "- ") {
			// Bullet point
			bulletText := strings.TrimSpace(line[2:]) // Remove "- "

			// Replace [L1], [L2] references with newsletter subject and date
			for linkKey, info := range linkMap {
				if strings.Contains(bulletText, linkKey) {
					formattedDate := utils.FormatEmailDate(info.date)
					sourceHTML := fmt.Sprintf(`<span class="source">(%s - %s)</span>`,
						utils.HtmlEscape(info.subject),
						utils.HtmlEscape(formattedDate))
					bulletText = strings.Replace(bulletText, linkKey, sourceHTML, -1)
				}
			}

			html.WriteString(fmt.Sprintf("    <li>%s</li>\n", bulletText))
		}
	}

	// Close final section if open
	if inSection {
		html.WriteString("  </ul>\n")
	}

	return html.String()
}

func (p *Processor) detectSections(text string) []string {
	var sections []string
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "===") && strings.HasSuffix(line, "===") {
			sectionName := strings.TrimSpace(strings.Trim(line, "="))
			sections = append(sections, sectionName)
		}
	}

	return sections
}

func (p *Processor) buildCompleteHTML(content string, digestType string) string {
	date := time.Now().Format("2006-01-02")

	var html strings.Builder

	// HTML document structure
	html.WriteString("<!DOCTYPE html>\n")
	html.WriteString("<html>\n")
	html.WriteString("<head>\n")
	html.WriteString("  <meta charset=\"utf-8\">\n")
	html.WriteString("  <title>Weekly Digest - " + date + "</title>\n")
	html.WriteString("  <style>\n")
	html.WriteString("    body{font-family:-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica,Arial,sans-serif;line-height:1.5;color:#111;max-width:800px;margin:0 auto;padding:20px}\n")
	html.WriteString("    h1{font-size:24px;margin:0 0 16px;color:#333}\n")
	html.WriteString("    h2{font-size:18px;margin:24px 0 12px;border-bottom:2px solid #eee;padding-bottom:6px;color:#444}\n")
	html.WriteString("    h3{font-size:14px;margin:16px 0 8px;color:#666}\n")
	html.WriteString("    ul{margin:0 0 16px 20px;padding:0}\n")
	html.WriteString("    li{margin:8px 0;line-height:1.4}\n")
	html.WriteString("    .meta{color:#888;font-size:14px;margin-bottom:20px;font-style:italic}\n")
	html.WriteString("    table{width:100%;border-collapse:collapse;margin:20px 0;font-size:14px}\n")
	html.WriteString("    th,td{text-align:left;padding:8px 12px;border:1px solid #ddd}\n")
	html.WriteString("    th{background:#f8f9fa;font-weight:600}\n")
	html.WriteString("    .small{font-size:12px;color:#666}\n")
	html.WriteString("    .footer{margin-top:40px;padding-top:20px;border-top:2px solid #eee}\n")
	html.WriteString("    a{color:#0066cc;text-decoration:none}\n")
	html.WriteString("    a:hover{text-decoration:underline}\n")
	html.WriteString("    pre{white-space:pre-wrap;word-wrap:break-word;background:#f8f9fa;padding:12px;border-radius:4px}\n")
	html.WriteString("    .source{color:#666;font-size:0.9em;font-style:italic}\n")
	html.WriteString("  </style>\n")
	html.WriteString("</head>\n")
	html.WriteString("<body>\n")

	// Header with digest type
	var headerText string
	switch digestType {
	case "LinkedIn":
		headerText = "LinkedIn Industry Digest"
	case "Newsletter":
		headerText = "Newsletter Digest"
	case "Combined":
		headerText = "Weekly Digest"
	default:
		headerText = "Weekly Digest"
	}

	html.WriteString("  <h1>" + headerText + " (" + date + ")</h1>\n")

	// Meta description based on content type
	var metaText string
	switch digestType {
	case "LinkedIn":
		metaText = "Industry insights from LinkedIn: Product Management → Healthcare → Software Architecture → Team Organization → AI"
	case "Newsletter":
		metaText = "Newsletter summary: Product Management → Healthcare → Software Architecture → Team Organization → AI"
	case "Combined":
		metaText = "Combined insights from newsletters and LinkedIn: Product Management → Healthcare → Software Architecture → Team Organization → AI"
	default:
		metaText = "Prioritized: Product Management → Healthcare → Software Architecture → Team Organization → AI"
	}

	html.WriteString("  <div class=\"meta\">" + metaText + "</div>\n")

	// Content from OpenAI
	html.WriteString("  <div class=\"content\">\n")
	html.WriteString(content)
	html.WriteString("\n  </div>\n")

	// We'll add footer later in the main process
	html.WriteString("</body>\n")
	html.WriteString("</html>")

	return html.String()
}

func (p *Processor) createFallbackHTML(perSummaries []string, digestType string) string {
	var fb strings.Builder
	date := time.Now().Format("2006-01-02")
	fb.WriteString("<html><body><h1>" + digestType + " Digest (Fallback) ")
	fb.WriteString(date)
	fb.WriteString("</h1><pre>")
	fb.WriteString(utils.HtmlEscape(strings.Join(perSummaries, Separator)))
	fb.WriteString("</pre></body></html>")
	return fb.String()
}

func (p *Processor) createVerificationFooter(items []*models.Newsletter, linkedInCount int) string {
	var sb strings.Builder

	sb.WriteString("<!-- FOOTER START -->\n")
	sb.WriteString("<div style=\"clear:both;margin-top:40px;padding-top:20px;border-top:2px solid #eee;font-size:14px;\">\n")
	sb.WriteString("  <h3 style=\"margin:0 0 15px 0;color:#666;font-size:16px;\">Content Sources</h3>\n")

	if len(items) > 0 {
		sb.WriteString(fmt.Sprintf("  <p style=\"margin:0 0 20px 0;color:#888;font-size:14px;\">Newsletters: %d processed. LinkedIn posts: %d processed.</p>\n", len(items), linkedInCount))

		for i, it := range items {
			sb.WriteString("  <div style=\"margin-bottom:15px;padding:10px;background:#f9f9f9;border-radius:4px;\">\n")
			sb.WriteString(fmt.Sprintf("    <div style=\"font-weight:bold;margin-bottom:5px;\">#%d Newsletter: %s</div>\n", i+1, utils.HtmlEscape(it.Subject)))
			sb.WriteString(fmt.Sprintf("    <div style=\"color:#666;margin-bottom:5px;\">From: %s</div>\n", utils.HtmlEscape(it.From)))

			if len(it.Links) > 0 {
				sb.WriteString("    <div>Sample link: ")
				sb.WriteString(fmt.Sprintf("<a href=\"%s\" style=\"color:#0066cc;text-decoration:underline;\">%s</a>", utils.HtmlEscape(it.Links[0]), utils.HtmlEscape(it.Links[0])))
				sb.WriteString("</div>\n")
			} else {
				sb.WriteString("    <div>No links available</div>\n")
			}

			sb.WriteString("  </div>\n")
		}
	} else {
		sb.WriteString(fmt.Sprintf("  <p style=\"margin:0 0 20px 0;color:#888;font-size:14px;\">LinkedIn-only digest: %d professional posts processed (promotional content filtered out).</p>\n", linkedInCount))
	}

	sb.WriteString("</div>\n")
	sb.WriteString("<!-- FOOTER END -->\n")

	return sb.String()
}

func (p *Processor) appendPerEmailSample(html string, per []string) string {
	var sb strings.Builder

	// Only add to existing HTML if provided
	if html != "" {
		sb.WriteString(html)
	}

	sb.WriteString(`<hr/><h2 class="small">Per-email bullets (sample)</h2>`)
	max := 10
	if len(per) < max {
		max = len(per)
	}
	for i := 0; i < max; i++ {
		title := "Item"
		if strings.HasPrefix(per[i], "### ") {
			first := strings.SplitN(per[i], "\n", 2)[0]
			title = strings.TrimPrefix(first, "### ")
		}
		sb.WriteString(fmt.Sprintf("<h3 class='small'>%s</h3><pre class='small'>%s</pre>", utils.HtmlEscape(title), utils.HtmlEscape(per[i])))
	}
	return sb.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
