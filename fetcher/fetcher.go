package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	html2text "github.com/jaytaylor/html2text"
)

type ContentFetcher struct {
	client *http.Client
}

func New() *ContentFetcher {
	return &ContentFetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
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

func (f *ContentFetcher) FetchLinkedInContent(url string) (string, error) {
	fmt.Printf("Fetching LinkedIn content from: %s\n", url)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	
	// Set headers to mimic a real browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Connection", "keep-alive")
	
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
	text, err := html2text.FromString(string(body))
	if err != nil {
		return "", err
	}
	
	// Clean and extract the main content
	return f.extractLinkedInArticleContent(text), nil
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
