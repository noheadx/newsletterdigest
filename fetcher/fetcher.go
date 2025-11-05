package fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	html2text "github.com/jaytaylor/html2text"
)

type ContentFetcher struct {
	client   *http.Client
	cacheDir string
}

type LinkedInPost struct {
	Text      string
	Author    string
	URL       string
	Timestamp string
	Hashtags  []string
	Score     int // Quality score for ranking
}

func New() *ContentFetcher {
	// Create cache directory
	cacheDir := filepath.Join(os.TempDir(), "forumscout_cache")
	os.MkdirAll(cacheDir, 0755)

	return &ContentFetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		cacheDir: cacheDir,
	}
}

var linkedinURLPattern = regexp.MustCompile(`https://(?:www\.)?linkedin\.com/pulse/[^/\s]+/?`)

func (f *ContentFetcher) ShouldFetchContent(text string, links []string) (bool, string) {
	// Check if this looks like a LinkedIn newsletter teaser
	if !f.isLinkedInNewsletter(text, links) {
		return false, ""
	}

	// Find the LinkedIn article URL
	for _, link := range links {
		if linkedinURLPattern.MatchString(link) {
			return true, link
		}
	}

	return false, ""
}

func (f *ContentFetcher) isLinkedInNewsletter(text string, links []string) bool {
	// Check for common indicators of LinkedIn newsletter teasers
	indicators := []string{
		"read more on linkedin",
		"continue reading",
		"full article",
		"read the full",
		"linkedin newsletter",
	}

	textLower := strings.ToLower(text)
	for _, indicator := range indicators {
		if strings.Contains(textLower, indicator) {
			return true
		}
	}

	// Also check if there's a LinkedIn pulse URL in the links
	for _, link := range links {
		if linkedinURLPattern.MatchString(link) && len(text) < 800 { // Short text + LinkedIn link = likely teaser
			return true
		}
	}

	return false
}

func (f *ContentFetcher) FetchLinkedInContent(rawURL string) (string, error) {
	// Clean the LinkedIn URL - remove everything after the first '?'
	cleanURL := f.cleanLinkedInURL(rawURL)

	req, err := http.NewRequest("GET", cleanURL, nil)
	if err != nil {
		return "", err
	}

	// Set headers to mimic a real browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Connection", "keep-alive")
	// Don't request compressed content to avoid binary issues
	// req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Convert HTML to text
	bodyStr := string(body)
	text, err := html2text.FromString(bodyStr)
	if err != nil {
		// Fallback: basic HTML tag removal
		text = f.basicHTMLStrip(bodyStr)
	}

	// Clean and extract the main content
	cleanText := f.extractLinkedInArticleContent(text)

	return cleanText, nil
}

// safeStringPreview returns a safe preview of a string, handling potential binary content
func (f *ContentFetcher) safeStringPreview(s string, maxLen int) string {
	// Check if string contains binary data (non-printable characters)
	printableCount := 0
	for _, r := range s {
		if r >= 32 && r <= 126 || r == '\n' || r == '\r' || r == '\t' {
			printableCount++
		}
	}

	// If less than 80% printable, it's likely binary
	if len(s) > 0 && float64(printableCount)/float64(len(s)) < 0.8 {
		return "[BINARY/ENCODED CONTENT]"
	}

	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// basicHTMLStrip removes HTML tags as a fallback when html2text fails
func (f *ContentFetcher) basicHTMLStrip(html string) string {
	// Remove script and style tags completely
	scriptRe := regexp.MustCompile(`(?i)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")

	styleRe := regexp.MustCompile(`(?i)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	// Remove HTML tags
	tagRe := regexp.MustCompile(`<[^>]*>`)
	text := tagRe.ReplaceAllString(html, " ")

	// Clean up whitespace
	spaceRe := regexp.MustCompile(`\s+`)
	text = spaceRe.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

// cleanLinkedInURL removes query parameters and fragments from LinkedIn URLs
func (f *ContentFetcher) cleanLinkedInURL(rawURL string) string {
	// Parse the URL
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL // Return original if parsing fails
	}

	// Remove query parameters and fragment
	u.RawQuery = ""
	u.Fragment = ""

	return u.String()
}

// FetchLinkedInHashtagContent fetches recent posts from LinkedIn based on hashtags
func (f *ContentFetcher) FetchLinkedInHashtagContent(hashtags []string, maxPosts int, fetchFullContent bool) ([]LinkedInPost, error) {
	fmt.Printf("Fetching LinkedIn posts for hashtags: %v\n", hashtags)

	var allPosts []LinkedInPost

	for _, hashtag := range hashtags {
		// Clean hashtag (remove # if present)
		cleanTag := strings.TrimPrefix(hashtag, "#")

		posts, err := f.fetchHashtagPosts(cleanTag, maxPosts/len(hashtags), fetchFullContent)
		if err != nil {
			continue
		}

		allPosts = append(allPosts, posts...)
	}

	// Sort by relevance/recency and limit total
	if len(allPosts) > maxPosts {
		allPosts = allPosts[:maxPosts]
	}

	return allPosts, nil
}

func (f *ContentFetcher) fetchHashtagPosts(hashtag string, limit int, fetchFullContent bool) ([]LinkedInPost, error) {
	// Check cache first
	if cached, found := f.getCachedResponse(hashtag); found {
		return f.processPosts(cached, hashtag, limit, fetchFullContent), nil
	}

	forumScoutKey := os.Getenv("FORUMSCOUT_API_KEY")
	if forumScoutKey == "" {
		return f.createMockPosts(hashtag, limit), nil
	}

	// Build API request with keyword parameter
	baseURL := "https://forumscout.app/api/linkedin_search"
	params := url.Values{}
	params.Add("keyword", hashtag)

	apiURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authorization header
	req.Header.Set("X-API-Key", forumScoutKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NewsletterDigest/1.0")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	// Enhanced error handling
	switch resp.StatusCode {
	case 429:
		return nil, fmt.Errorf("rate limit exceeded, try again later")
	case 401:
		return nil, fmt.Errorf("invalid API key")
	case 403:
		return nil, fmt.Errorf("access forbidden - check API permissions")
	case 200:
		// Success, continue
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the JSON response as array
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var posts []ForumScoutPost
	if err := json.Unmarshal(body, &posts); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	// Cache the response
	f.cacheResponse(hashtag, posts)

	// Add rate limiting
	time.Sleep(1 * time.Second)

	return f.processPosts(posts, hashtag, limit, fetchFullContent), nil
}

// processPosts converts ForumScout posts to LinkedInPost format with scoring and filtering
func (f *ContentFetcher) processPosts(posts []ForumScoutPost, hashtag string, limit int, fetchFullContent bool) []LinkedInPost {
	var linkedInPosts []LinkedInPost

	for _, post := range posts {
		// Filter by post age (only last 7 days)
		if !f.isRecentPost(post.Date) {
			continue
		}

		// Clean the LinkedIn URL before using it
		cleanURL := f.cleanLinkedInURL(post.URL)

		// Ensure snippet is clean text
		snippet := f.cleanTextContent(post.Snippet)

		// Create LinkedInPost with quality scoring
		linkedInPost := LinkedInPost{
			Text:      snippet,
			Author:    f.cleanTextContent(post.Author),
			URL:       cleanURL,
			Timestamp: post.Date,
			Hashtags:  []string{hashtag},
			Score:     f.scorePost(post),
		}

		// Debug: Log the snippet content
		// Process post snippet

		// If full content fetching is enabled, get complete post content from LinkedIn URL
		if fetchFullContent && len(snippet) < 300 {
			if fullContent, err := f.FetchLinkedInContent(cleanURL); err == nil && len(fullContent) > len(snippet) {
				// Ensure full content is also clean text
				cleanFullContent := f.cleanTextContent(fullContent)
				linkedInPost.Text = cleanFullContent
			}
		}

		linkedInPosts = append(linkedInPosts, linkedInPost)
	}

	// Sort by score (highest first)
	for i := 0; i < len(linkedInPosts)-1; i++ {
		for j := 0; j < len(linkedInPosts)-1-i; j++ {
			if linkedInPosts[j].Score < linkedInPosts[j+1].Score {
				linkedInPosts[j], linkedInPosts[j+1] = linkedInPosts[j+1], linkedInPosts[j]
			}
		}
	}

	// Limit results
	if len(linkedInPosts) > limit {
		linkedInPosts = linkedInPosts[:limit]
	}

	return linkedInPosts
}

// cleanTextContent ensures text content is properly decoded and cleaned
func (f *ContentFetcher) cleanTextContent(text string) string {
	// Remove any null bytes or other binary artifacts
	text = strings.ReplaceAll(text, "\x00", "")

	// Handle common HTML entities
	replacements := map[string]string{
		"&amp;":   "&",
		"&lt;":    "<",
		"&gt;":    ">",
		"&quot;":  "\"",
		"&apos;":  "'",
		"&nbsp;":  " ",
		"&#8217;": "'",
		"&#8220;": "\"",
		"&#8221;": "\"",
		"&#8230;": "...",
	}

	for entity, replacement := range replacements {
		text = strings.ReplaceAll(text, entity, replacement)
	}

	// Clean up whitespace
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	return text
}

// isRecentPost filters posts to only include those from the last 7 days
func (f *ContentFetcher) isRecentPost(dateStr string) bool {
	postDate, err := time.Parse("2006-01-02 15:04:05", dateStr)
	if err != nil {
		// Try alternative format
		if postDate, err = time.Parse("2006-01-02", dateStr); err != nil {
			return true // Include if we can't parse date
		}
	}
	return time.Since(postDate) <= 7*24*time.Hour
}

// scorePost assigns a quality score to posts for ranking
func (f *ContentFetcher) scorePost(post ForumScoutPost) int {
	score := 0

	// Content length scoring
	if len(post.Snippet) > 100 {
		score += 2
	}
	if len(post.Snippet) > 200 {
		score += 1
	}

	// Authority indicators in author name
	authorLower := strings.ToLower(post.Author)
	if strings.Contains(authorLower, "ceo") || strings.Contains(authorLower, "cto") ||
		strings.Contains(authorLower, "founder") || strings.Contains(authorLower, "director") {
		score += 3
	}
	if strings.Contains(authorLower, "manager") || strings.Contains(authorLower, "lead") ||
		strings.Contains(authorLower, "senior") {
		score += 2
	}
	if strings.Contains(authorLower, "dr.") || strings.Contains(authorLower, "phd") {
		score += 2
	}

	// Title quality
	if len(post.Title) > 20 {
		score += 1
	}
	if len(post.Title) > 50 {
		score += 1
	}

	// Professional keywords in content
	contentLower := strings.ToLower(post.Title + " " + post.Snippet)
	professionalKeywords := []string{
		"strategy", "innovation", "insights", "analysis", "research",
		"best practices", "framework", "methodology", "trends", "future",
	}
	for _, keyword := range professionalKeywords {
		if strings.Contains(contentLower, keyword) {
			score += 1
		}
	}

	return score
}

// getCachedResponse retrieves cached API response if available and not expired
func (f *ContentFetcher) getCachedResponse(hashtag string) ([]ForumScoutPost, bool) {
	cacheFile := filepath.Join(f.cacheDir, fmt.Sprintf("forumscout_%s.json", hashtag))

	// Check if cache file exists and is less than 1 hour old
	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) < 1*time.Hour {
			data, err := os.ReadFile(cacheFile)
			if err == nil {
				var posts []ForumScoutPost
				if json.Unmarshal(data, &posts) == nil {
					return posts, true
				}
			}
		}
	}

	return nil, false
}

// cacheResponse saves API response to cache
func (f *ContentFetcher) cacheResponse(hashtag string, posts []ForumScoutPost) {
	cacheFile := filepath.Join(f.cacheDir, fmt.Sprintf("forumscout_%s.json", hashtag))

	if data, err := json.Marshal(posts); err == nil {
		os.WriteFile(cacheFile, data, 0644)
	}
}

// ForumScout API response structures based on actual API format
type ForumScoutPost struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Date    string `json:"date"`
	Author  string `json:"author"`
	Source  string `json:"source"`
	Domain  string `json:"domain"`
	Snippet string `json:"snippet"`
}

// Fallback function for testing without API key
func (f *ContentFetcher) createMockPosts(hashtag string, limit int) []LinkedInPost {
	posts := []LinkedInPost{
		{
			Text:      fmt.Sprintf("Exciting developments in %s! New research shows significant progress in digital transformation and innovation strategies. Companies are seeing improved ROI through strategic implementation.", hashtag),
			Author:    "Industry Expert",
			URL:       fmt.Sprintf("https://linkedin.com/posts/example-%s-1", hashtag),
			Timestamp: time.Now().Add(-6 * time.Hour).Format("2006-01-02T15:04:05Z"),
			Hashtags:  []string{hashtag},
			Score:     5,
		},
		{
			Text:      fmt.Sprintf("Key insights from the latest %s conference: Focus on user-centric design and data-driven decision making is becoming crucial for competitive advantage.", hashtag),
			Author:    "Tech Leader",
			URL:       fmt.Sprintf("https://linkedin.com/posts/example-%s-2", hashtag),
			Timestamp: time.Now().Add(-12 * time.Hour).Format("2006-01-02T15:04:05Z"),
			Hashtags:  []string{hashtag},
			Score:     4,
		},
		{
			Text:      fmt.Sprintf("Breaking: Major industry announcement in %s sector. New partnerships and funding rounds are accelerating innovation across the ecosystem.", hashtag),
			Author:    "Venture Capitalist",
			URL:       fmt.Sprintf("https://linkedin.com/posts/example-%s-3", hashtag),
			Timestamp: time.Now().Add(-18 * time.Hour).Format("2006-01-02T15:04:05Z"),
			Hashtags:  []string{hashtag},
			Score:     6,
		},
	}

	if limit < len(posts) {
		posts = posts[:limit]
	}

	return posts
}

func (f *ContentFetcher) extractLinkedInArticleContent(text string) string {
	lines := strings.Split(text, "\n")
	var contentLines []string
	inArticle := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip header/navigation content
		if strings.Contains(strings.ToLower(line), "linkedin") &&
			(strings.Contains(strings.ToLower(line), "home") ||
				strings.Contains(strings.ToLower(line), "feed") ||
				strings.Contains(strings.ToLower(line), "network")) {
			continue
		}

		// Look for article start indicators
		if !inArticle && (len(line) > 50 || strings.Contains(line, ".") || strings.Contains(line, ",")) {
			inArticle = true
		}

		// Skip footer content
		if strings.Contains(strings.ToLower(line), "follow") && strings.Contains(strings.ToLower(line), "linkedin") {
			break
		}
		if strings.Contains(strings.ToLower(line), "connect with") {
			break
		}

		if inArticle {
			contentLines = append(contentLines, line)
		}
	}

	content := strings.Join(contentLines, "\n")

	// Clean up the content
	content = regexp.MustCompile(`\n{3,}`).ReplaceAllString(content, "\n\n")
	content = strings.TrimSpace(content)

	// Limit content length to avoid overwhelming the summarizer
	if len(content) > 8000 {
		content = content[:8000] + "... [content truncated]"
	}

	return content
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
