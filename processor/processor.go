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

	if len(perSummaries) == 0 {
		return "", nil, fmt.Errorf("no usable content extracted")
	}

	finalHTML, err := p.synthesizeFinal(ctx, perSummaries, processedItems)
	if err != nil {
		// fallback to raw bullets
		return p.createFallbackHTML(perSummaries), processedItems, nil
	}

	// Add verification footer and optional sample
	if v := validator.ValidateOutput(finalHTML); v != "" {
		log.Printf("validator: %s", v)
	}

	// Insert footer before closing body tag (we know the structure now)
	if p.config.ShowFooter {
		footerHTML := "\n  <div class=\"footer\">\n" + p.createVerificationFooter(processedItems)
		if p.config.AppendSample {
			footerHTML += p.appendPerEmailSample("", perSummaries)
		}
		footerHTML += "\n  </div>\n"

		// Insert before </body>
		finalHTML = strings.Replace(finalHTML, "</body>", footerHTML+"</body>", 1)
	}

	return finalHTML, processedItems, nil
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

func (p *Processor) synthesizeFinal(ctx context.Context, perSumm []string, meta []*models.Newsletter) (string, error) {
	// Build link index
	var li []string
	for _, m := range meta {
		if len(m.Links) == 0 {
			continue
		}
		var lines []string
		for j, u := range m.Links {
			lines = append(lines, fmt.Sprintf("[L%d] %s", j+1, u))
		}
		li = append(li, fmt.Sprintf("%s\n%s", m.Subject, strings.Join(lines, "\n")))
	}
	linkIndex := strings.Join(li, "\n\n")
	joined := strings.Join(perSumm, Separator)

	system := p.config.PromptFinal
	user := "Combine and rank the following per-email bullet summaries into a weekly digest, grouped in this EXACT order:\n" +
		"1) === Product Management ===\n" +
		"2) === Healthcare ===\n" +
		"3) === Architecture ===\n" +
		"4) === Team Organization ===\n" +
		"5) === AI ===\n" +
		"Rules:\n" +
		"- MUST generate ALL 5 sections above in exact order, even if brief\n" +
		"- Follow each section header with bullet points starting with '- '\n" +
		"- If no content for a section, add '- No significant updates this week'\n" +
		"- Merge duplicates, preserve key facts/metrics, include short source names\n" +
		"- Keep [L1], [L2] references as-is in the text for link replacement\n" +
		"- No HTML, Markdown, or special formatting - just plain text\n\n" +
		"=== PER-EMAIL SUMMARIES ===\n" + joined +
		"\n\n=== LINK INDEX (map [L*] to URLs) ===\n" + linkIndex + "\n\n" +
		"Generate exactly 5 sections as specified above."

	messages := []openai.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	out, err := p.openaiClient.Chat(ctx, p.config.FinalModel, messages, 0.1, 2000)
	if err != nil {
		return "", err
	}

	fmt.Printf("Raw ChatGPT plain text length: %d chars\n", len(out))
	fmt.Printf("Raw ChatGPT plain text (first 800 chars): %s...\n", out[:min(800, len(out))])

	// Show what sections were detected
	sections := p.detectSections(out)
	fmt.Printf("Detected sections: %v\n", sections)

	// Parse the plain text and convert to HTML
	htmlContent := p.parseTextToHTML(strings.TrimSpace(out), meta)

	// Build the complete HTML document ourselves
	finalHTML := p.buildCompleteHTML(htmlContent)

	fmt.Printf("Final HTML length: %d chars\n", len(finalHTML))

	return finalHTML, nil
}

func (p *Processor) parseTextToHTML(text string, meta []*models.Newsletter) string {
	var html strings.Builder

	// Build link map for [L1], [L2] replacement
	linkMap := make(map[string]string)
	linkCounter := 1
	for _, m := range meta {
		for _, link := range m.Links {
			linkKey := fmt.Sprintf("[L%d]", linkCounter)
			linkMap[linkKey] = link
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

			// Replace [L1], [L2] references with actual links
			for linkKey, linkURL := range linkMap {
				if strings.Contains(bulletText, linkKey) {
					linkHTML := fmt.Sprintf(`<a href="%s">source</a>`, utils.HtmlEscape(linkURL))
					bulletText = strings.Replace(bulletText, linkKey, linkHTML, -1)
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

func (p *Processor) buildCompleteHTML(content string) string {
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
	html.WriteString("  </style>\n")
	html.WriteString("</head>\n")
	html.WriteString("<body>\n")

	// Header
	html.WriteString("  <h1>Weekly Digest (" + date + ")</h1>\n")
	html.WriteString("  <div class=\"meta\">Prioritized: Product Management → Healthcare → Architecture → Team Organization (AI only if relevant)</div>\n")

	// Content from OpenAI
	html.WriteString("  <div class=\"content\">\n")
	html.WriteString(content)
	html.WriteString("\n  </div>\n")

	// We'll add footer later in the main process
	html.WriteString("</body>\n")
	html.WriteString("</html>")

	return html.String()
}

func (p *Processor) createFallbackHTML(perSummaries []string) string {
	var fb strings.Builder
	date := time.Now().Format("2006-01-02")
	fb.WriteString("<html><body><h1>Weekly Digest (Fallback) ")
	fb.WriteString(date)
	fb.WriteString("</h1><pre>")
	fb.WriteString(utils.HtmlEscape(strings.Join(perSummaries, Separator)))
	fb.WriteString("</pre></body></html>")
	return fb.String()
}

func (p *Processor) createVerificationFooter(items []*models.Newsletter) string {
	var sb strings.Builder

	sb.WriteString("<!-- FOOTER START -->\n")
	sb.WriteString("<div style=\"clear:both;margin-top:40px;padding-top:20px;border-top:2px solid #eee;font-size:14px;\">\n")
	sb.WriteString("  <h3 style=\"margin:0 0 15px 0;color:#666;font-size:16px;\">Verification</h3>\n")
	sb.WriteString(fmt.Sprintf("  <p style=\"margin:0 0 20px 0;color:#888;font-size:14px;\">Processed %d newsletter(s). Order below is input order, not priority.</p>\n", len(items)))

	for i, it := range items {
		sb.WriteString("  <div style=\"margin-bottom:15px;padding:10px;background:#f9f9f9;border-radius:4px;\">\n")
		sb.WriteString(fmt.Sprintf("    <div style=\"font-weight:bold;margin-bottom:5px;\">#%d: %s</div>\n", i+1, utils.HtmlEscape(it.Subject)))
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
