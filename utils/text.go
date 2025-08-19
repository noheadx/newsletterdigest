package utils

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"

	html2text "github.com/jaytaylor/html2text"
	gmail "google.golang.org/api/gmail/v1"
)

var (
	linkBlacklist = []*regexp.Regexp{
		regexp.MustCompile(`unsubscribe`),
		regexp.MustCompile(`manage[-_ ]?preferences`),
		regexp.MustCompile(`update[-_ ]?subscription`),
		regexp.MustCompile(`/privacy`),
		regexp.MustCompile(`/terms`),
		regexp.MustCompile(`/legal`),
		regexp.MustCompile(`^mailto:`),
		regexp.MustCompile(`#`),
	}
	urlInTextRe = regexp.MustCompile(`https?://[^\s<>"\)]{5,}`)
	hrefRe      = regexp.MustCompile(`href=['"](https?://[^'"]+)['"]`)
	unsubTrimRe = []*regexp.Regexp{
		regexp.MustCompile(`(?i)unsubscribe here.*?$`),
		regexp.MustCompile(`(?i)manage preferences.*?$`),
		regexp.MustCompile(`(?i)update subscription.*?$`),
		regexp.MustCompile(`(?i)privacy policy.*?$`),
		regexp.MustCompile(`(?i)Â©\s*\d{4}.*?$`),
		regexp.MustCompile(`(?i)(View in browser|Forward to a friend).*?$`),
	}
)

func CleanText(s string) string {
	out := s
	for _, re := range unsubTrimRe {
		out = re.ReplaceAllString(out, "")
	}
	out = strings.ReplaceAll(out, " \n", "\n")
	out = regexp.MustCompile(`\n{3,}`).ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

func ExtractLinks(htmlOrText string, isHTML bool) []string {
	var urls []string
	if isHTML {
		for _, m := range hrefRe.FindAllStringSubmatch(htmlOrText, -1) {
			urls = append(urls, m[1])
		}
	}
	for _, m := range urlInTextRe.FindAllStringSubmatch(htmlOrText, -1) {
		urls = append(urls, m[0])
	}
	
	// Clean & filter
	out := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, u := range urls {
		u = strings.TrimSpace(strings.TrimRight(u, ").,;"))
		l := strings.ToLower(u)
		bad := false
		for _, bl := range linkBlacklist {
			if bl.MatchString(l) {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		
		// Strip tracking params
		pu, err := url.Parse(u)
		if err == nil {
			q := pu.Query()
			for k := range q {
				kl := strings.ToLower(k)
				if strings.HasPrefix(kl, "utm_") || kl == "mc_eid" || kl == "mc_cid" {
					q.Del(k)
				}
			}
			pu.RawQuery = q.Encode()
			u = pu.String()
		}
		
		if !seen[u] {
			out = append(out, u)
			seen[u] = true
			if len(out) >= 3 {
				break
			}
		}
	}
	return out
}

func PartsToTextAndLinks(p *gmail.MessagePart) (string, []string) {
	if p == nil {
		return "", nil
	}
	var texts []string
	var links []string

	var walk func(part *gmail.MessagePart)
	walk = func(part *gmail.MessagePart) {
		if len(part.Parts) > 0 {
			for _, sp := range part.Parts {
				walk(sp)
			}
			return
		}
		if part.Body != nil && part.Body.Data != "" {
			raw, _ := base64.URLEncoding.DecodeString(part.Body.Data)
			ct := strings.ToLower(part.MimeType)
			if strings.Contains(ct, "text/html") {
				if txt, err := html2text.FromString(string(raw)); err == nil {
					texts = append(texts, txt)
				} else {
					// Fallback to raw if conversion fails
					texts = append(texts, string(raw))
				}
				links = append(links, ExtractLinks(string(raw), true)...)
			} else if strings.Contains(ct, "text/plain") {
				texts = append(texts, string(raw))
				links = append(links, ExtractLinks(string(raw), false)...)
			}
		}
	}
	walk(p)
	return strings.Join(texts, "\n"), ExtractLinks(strings.Join(texts, "\n"), false)
}

func HtmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;",
	)
	return r.Replace(s)
}
