package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	// Import your credentials package
	"newsletterdigest_go/credentials"

	html2text "github.com/jaytaylor/html2text"
	gmail "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

const (
	GmailQueryDefault = `label:newsletter is:unread`
	MaxResults        = 80
	PerEmailMaxChars  = 6000
	PerEmailSleep     = 1100 * time.Millisecond
	RetryMax          = 5
	BackoffMin        = 1500 * time.Millisecond
	BackoffMax        = 6 * time.Second
	Separator         = "\n\n——————————————\n\n"
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
		regexp.MustCompile(`(?i)©\s*\d{4}.*?$`),
		regexp.MustCompile(`(?i)(View in browser|Forward to a friend).*?$`),
	}
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

/* ---------------- Gmail OAuth client (updated) ---------------- */

func getClient(ctx context.Context) (*http.Client, error) {
	store, err := credentials.NewStoreFromEnv()
	if err != nil {
		return nil, fmt.Errorf("init credential store: %w", err)
	}

	client, err := store.GetOAuthClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("get oauth client: %w", err)
	}

	return client, nil
}

// Setup command to initialize credentials
func setupCredentials() error {
	store, err := credentials.NewStoreFromEnv()
	if err != nil {
		return fmt.Errorf("init credential store: %w", err)
	}

	credPath := os.Getenv("GOOGLE_CREDENTIALS_FILE")
	if credPath == "" {
		return errors.New("GOOGLE_CREDENTIALS_FILE environment variable required for setup")
	}

	if err := store.SetupFromFile(credPath); err != nil {
		return fmt.Errorf("setup credentials: %w", err)
	}

	fmt.Println("Credentials stored securely!")
	fmt.Println("You can now delete the original credentials.json file")
	fmt.Println("Set CREDENTIALS_PASSPHRASE environment variable for future runs")
	return nil
}

/* ---------------- Models & OpenAI (unchanged) ---------------- */

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type chatReq struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_completion_tokens"`
}
type chatResp struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func openAIChat(ctx context.Context, model string, messages []chatMessage, temp float64, maxTok int) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("missing OPENAI_API_KEY")
	}
	fmt.Println("Calling ChatGPT: model", model, " temperature: ", temp, " max_tokens:", maxTok)
	reqBody := chatReq{
		Model:       model,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   maxTok,
	}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	var lastErr error
	for attempt := 1; attempt <= RetryMax; attempt++ {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == 200 {
				var cr chatResp
				if err := json.Unmarshal(body, &cr); err != nil {
					return "", err
				}
				if len(cr.Choices) == 0 {
					return "", errors.New("no choices")
				}
				return cr.Choices[0].Message.Content, nil
			}
			// Retry on 429/5xx
			if resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode <= 599) {
				lastErr = fmt.Errorf("openai status %d: %s", resp.StatusCode, string(body))
			} else {
				return "", fmt.Errorf("openai status %d: %s", resp.StatusCode, string(body))
			}
		}
		// backoff
		sleep := time.Duration(math.Min(float64(BackoffMax), float64(BackoffMin)*math.Pow(2, float64(attempt-1)))) + time.Duration(rand.Intn(700))*time.Millisecond
		time.Sleep(sleep)
	}
	return "", lastErr
}

/* ---------------- Utilities (unchanged from your original) ---------------- */

type msgItem struct {
	ID      string
	Subject string
	From    string
	Date    string
	Text    string
	Links   []string
}

func cleanText(s string) string {
	out := s
	for _, re := range unsubTrimRe {
		out = re.ReplaceAllString(out, "")
	}
	out = strings.ReplaceAll(out, " \n", "\n")
	out = regexp.MustCompile(`\n{3,}`).ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

func trim(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func extractLinks(htmlOrText string, isHTML bool) []string {
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

/* ---------------- Gmail helpers (unchanged) ---------------- */

func partsToTextAndLinks(p *gmail.MessagePart) (string, []string) {
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
				links = append(links, extractLinks(string(raw), true)...)
			} else if strings.Contains(ct, "text/plain") {
				texts = append(texts, string(raw))
				links = append(links, extractLinks(string(raw), false)...)
			}
		}
	}
	walk(p)
	return strings.Join(texts, "\n"), extractLinks(strings.Join(texts, "\n"), false) // ensure plain URLs too
}

func gmailSendHTML(ctx context.Context, svc *gmail.Service, to, subject, htmlBody string) error {
	var b strings.Builder
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n\r\n")
	// plain part
	// plain := "Your client does not support HTML. Please open in an HTML-capable client."
	// b.WriteString("--BOUNDARY\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
	// b.WriteString(plain + "\r\n")

	// html part
	b.WriteString("--BOUNDARY\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n")
	b.WriteString(htmlBody + "\r\n")
	b.WriteString("--BOUNDARY--")

	raw := base64.URLEncoding.EncodeToString([]byte(b.String()))
	_, err := svc.Users.Messages.Send("me", &gmail.Message{Raw: raw}).Do()
	return err
}

/* ---------------- Prompts (unchanged) ---------------- */

func systemPass1() string {
	return "You summarize single newsletters for a product executive. " +
		"Return 3–6 short bullets as plain text lines (no HTML/Markdown). " +
		"Priorities: 1) Product Management 2) Healthcare 3) Architecture 4) Team Organization. " +
		"Compress or omit pure AI hype unless it clearly impacts those four. No fluff."
}

func systemFinal() string {
	return "You assemble a concise weekly digest for a product executive. " +
		"Priorities: 1) Product Management 2) Healthcare 3) Architecture 4) Team Organization. " +
		"AI is lowest priority; include only if clearly relevant. Keep key figures and decisions. " +
		"OUTPUT STRICT, WELL-FORMED HTML ONLY (no Markdown). Use <h1>, <h2>, <ul>, <li>, <p>, <a>. " +
		"When you see [L1], [L2], etc., replace them with proper HTML anchors using the corresponding URLs supplied for that item. " +
		"Do not include Markdown code fences (```html). Output raw HTML only."
}

func htmlScaffold(date string) string {
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8">
<style>
body{font-family:-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica,Arial,sans-serif;line-height:1.5;color:#111}
h1{font-size:20px;margin:0 0 12px}
h2{font-size:16px;margin:18px 0 8px;border-bottom:1px solid #eee;padding-bottom:4px}
ul{margin:0 0 14px 18px;padding:0}
li{margin:6px 0}
.meta{color:#666;font-size:12px;margin-bottom:14px}
table{width:100%%;border-collapse:collapse}
th,td{text-align:left;padding:6px 8px;border-bottom:1px solid #eee;font-size:12px}
.small{font-size:12px;color:#444}
pre{white-space:pre-wrap;word-wrap:break-word}
</style></head><body>
<h1>Weekly Digest (%s)</h1>
<div class="meta">Prioritized: Product Management → Healthcare → Architecture → Team Organization (AI only if relevant)</div>

<h2>Product Management</h2><ul><li>…</li></ul>
<h2>Healthcare</h2><ul><li>…</li></ul>
<h2>Architecture</h2><ul><li>…</li></ul>
<h2>Team Organization</h2><ul><li>…</li></ul>
<h2>AI (only if relevant)</h2><ul><li>…</li></ul>
</body></html>`, date)
}

/* ---------------- Summarization (unchanged) ---------------- */

func summarizeSingle(ctx context.Context, model string, subj, from, date, body string, links []string) (string, error) {
	body = cleanText(body)
	if len(body) == 0 {
		return "", nil
	}
	if len(body) > PerEmailMaxChars {
		body = body[:PerEmailMaxChars]
	}
	var lb strings.Builder
	if len(links) > 0 {
		lb.WriteString("Relevant links:\n")
		for i := range links {
			lb.WriteString(fmt.Sprintf("- %s\n", links[i]))
		}
		lb.WriteString("\n")
	}
	user := fmt.Sprintf(
		"Subject: %s\nFrom: %s\nDate: %s\nSummarize this single newsletter into 3–6 crisp bullets. "+
			"Emphasize Product Management, Healthcare, Architecture, Team Organization. "+
			"Only include AI if it clearly impacts those. If a bullet references something with a URL, keep a short cue like [L1], [L2] inline (no HTML/Markdown), referring to the 'Relevant links' list.\n\n%s%s",
		subj, from, date, lb.String(), body,
	)
	msgs := []chatMessage{
		{Role: "system", Content: systemPass1()},
		{Role: "user", Content: user},
	}
	return openAIChat(ctx, model, msgs, 0.1, 300)
}

func synthesizeFinal(ctx context.Context, model string, perSumm []string, meta []msgItem) (string, error) {
	date := time.Now().Format("2006-01-02")

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

	// Don’t send a literal scaffold; tell the model what to produce.
	system := systemFinal()
	user := "Combine and rank the following per-email bullet summaries into a weekly digest, grouped strictly in this order:\n" +
		"1) Product Management  2) Healthcare  3) Architecture  4) Team Organization  (then an AI section ONLY if relevant).\n" +
		"Rules:\n" +
		"- Use WELL-FORMED HTML ONLY (no Markdown). Use <h1>, <h2>, <ul>, <li>, <p>, <a>.\n" +
		"- Fill each section with concrete bullets; if a section has no content, OMIT the section entirely.\n" +
		"- Merge duplicates, preserve key facts/metrics, include short source names.\n" +
		"- Replace [L1], [L2] cues with <a href=\"URL\">short source</a> using the link index.\n\n" +
		"=== PER-EMAIL SUMMARIES ===\n" + joined +
		"\n\n=== LINK INDEX (map [L*] to URLs) ===\n" + linkIndex + "\n\n" +
		"Do not include Markdown code fences (```html). Output raw HTML only.\n\n" +
		"Now produce the final HTML."

	msgs := []chatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	out, err := openAIChat(ctx, model, msgs, 0.2, 1700)
	fmt.Println("ChatGPT replied: ", out, " and ", err)
	out = strings.Replace(out, "```html", "<html", 1)
	out = stripCodeFences(out)
	out = normalizeHTMLSummary(out)
	fmt.Println("Normalized it looks like: ", out, " and ", err)

	if err != nil {
		return "", err
	}
	// Fallback if model didn’t emit HTML at all
	if !strings.Contains(strings.ToLower(out), "<html") {
		out = htmlScaffold(date) + "<p><pre>" + htmlEscape(out) + "</pre></p>"
		return out, nil
	}
	// Guard: if output looks like a template (mostly "…"), try a simpler re-ask once
	if looksLikeEmptyTemplate(out) {
		simple := []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: "Skip any template. Output full HTML with real bullets only; omit empty sections.\n\n" + user},
		}
		out2, err2 := openAIChat(ctx, model, simple, 0.2, 1700)
		if err2 == nil && strings.Contains(strings.ToLower(out2), "<html") && !looksLikeEmptyTemplate(out2) {
			return out2, nil
		}
	}
	return out, nil
}

var ellipsisLi = regexp.MustCompile(`(?i)<li>\s*…\s*</li>`)
var anyLi = regexp.MustCompile(`(?i)<li>.*?</li>`)

func looksLikeEmptyTemplate(html string) bool {
	total := len(anyLi.FindAllString(html, -1))
	empty := len(ellipsisLi.FindAllString(html, -1))
	// Consider it empty if most bullets are ellipses or there are zero real bullets
	return total == 0 || (empty >= total-1 && total > 0)
}

/* ---------------- Validation & extras (unchanged) ---------------- */

func verifyFooter(items []msgItem) string {
	var rows []string
	for i, it := range items {
		sample := ""
		if len(it.Links) > 0 {
			sample = fmt.Sprintf(`<a href="%s">link</a>`, it.Links[0])
		}
		rows = append(rows, fmt.Sprintf(
			"<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
			i+1, htmlEscape(it.Subject), htmlEscape(it.From), htmlEscape(it.Date), sample,
		))
	}
	return `<hr/><h2 class="small">Verification</h2><p class="small">Processed ` + fmt.Sprintf("%d", len(items)) +
		` newsletter(s). Order below is input order, not priority.</p>
<table><thead><tr><th>#</th><th>Subject</th><th>From</th><th>Date</th><th>Sample link</th></tr></thead><tbody>` +
		strings.Join(rows, "") + `</tbody></table>`
}

func appendPerEmailSample(html string, per []string) string {
	var sb strings.Builder
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
		sb.WriteString(fmt.Sprintf("<h3 class='small'>%s</h3><pre class='small'>%s</pre>", htmlEscape(title), htmlEscape(per[i])))
	}
	return html + sb.String()
}

func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;",
	)
	return r.Replace(s)
}

func validateOutput(html string) (errMsg string) {
	must := []string{"Product Management", "Healthcare", "Architecture", "Team Organization"}
	for _, h := range must {
		if !strings.Contains(html, "<h2>"+h+"</h2>") {
			return "Missing required section: " + h
		}
	}
	aiPos := strings.Index(html, "<h2>AI")
	if aiPos != -1 {
		pm := strings.Index(html, "<h2>Product Management</h2>")
		hc := strings.Index(html, "<h2>Healthcare</h2>")
		ar := strings.Index(html, "<h2>Architecture</h2>")
		to := strings.Index(html, "<h2>Team Organization</h2>")
		order := []int{pm, hc, ar, to, aiPos}
		ok := true
		prev := -1
		for _, p := range order {
			if p == -1 {
				continue
			}
			if p < prev {
				ok = false
				break
			}
			prev = p
		}
		if !ok {
			return "AI section appears before higher-priority sections."
		}
	}
	return ""
}

func normalizeHTMLSummary(html string) string {
	html = strings.TrimSpace(html)

	// Strip any accidental text after </html>
	if idx := strings.LastIndex(strings.ToLower(html), "</html>"); idx > 0 {
		html = html[:idx+7]
	}

	// Ensure root tags
	if !strings.Contains(strings.ToLower(html), "<html") {
		html = "<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>" +
			html + "</body></html>"
	}
	return html
}

var codeFence = regexp.MustCompile("(?s)```[a-zA-Z]*\\n(.*)```")

func stripCodeFences(s string) string {
	m := codeFence.FindStringSubmatch(s)
	if len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(s)
}

/* ---------------- Main (updated with credential package) ---------------- */

func main() {
	// Check for setup command
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := setupCredentials(); err != nil {
			log.Fatalf("setup failed: %v", err)
		}
		return
	}

	// Validate environment
	if err := credentials.ValidateEnvironment(); err != nil {
		log.Fatalf("environment validation failed: %v", err)
	}

	ctx := context.Background()
	httpClient, err := getClient(ctx)
	if err != nil {
		log.Fatalf("oauth: %v", err)
	}
	svc, err := gmail.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		log.Fatalf("gmail service: %v", err)
	}

	query := getenv("GMAIL_QUERY", GmailQueryDefault)
	call := svc.Users.Messages.List("me").Q(query).MaxResults(MaxResults)
	list, err := call.Do()
	if err != nil {
		log.Fatalf("list messages: %v", err)
	}
	if len(list.Messages) == 0 {
		log.Println("[digest] No unread newsletters found. Exiting.")
		return
	}

	smallModel := getenv("OPENAI_MODEL_SMALL", "gpt-4o-mini")
	finalModel := getenv("OPENAI_MODEL_FINAL", "gpt-4o")
	toEmail := os.Getenv("TO_EMAIL")
	if toEmail == "" {
		log.Fatal("TO_EMAIL not set")
	}

	perSummaries := []string{}
	processed := []msgItem{}

	for _, m := range list.Messages {
		full, err := svc.Users.Messages.Get("me", m.Id).Format("full").Do()
		if err != nil {
			log.Printf("get message %s: %v", m.Id, err)
			continue
		}
		hdr := map[string]string{}
		for _, h := range full.Payload.Headers {
			hdr[h.Name] = h.Value
		}
		subj := hdr["Subject"]
		from := hdr["From"]
		date := hdr["Date"]
		if subj == "" {
			subj = "(no subject)"
		}
		// normalize "From"
		if a, err := mail.ParseAddress(from); err == nil && a.Name != "" {
			from = a.Name + " <" + a.Address + ">"
		}

		text, links := partsToTextAndLinks(full.Payload)
		text = cleanText(text)
		if len(text) < 400 {
			continue
		}

		summary, err := summarizeSingle(ctx, smallModel, subj, from, date, text, links)
		if err != nil {
			log.Printf("summarize: %v", err)
			continue
		}
		perSummaries = append(perSummaries, "### "+subj+"\n"+summary+"\nLinks:\n"+strings.Join(links, "\n"))
		processed = append(processed, msgItem{ID: m.Id, Subject: subj, From: from, Date: date, Text: text, Links: links})

		time.Sleep(PerEmailSleep) // gentle throttle
	}

	if len(perSummaries) == 0 {
		log.Println("[digest] No usable content extracted. Exiting.")
		return
	}

	finalHTML, err := synthesizeFinal(ctx, finalModel, perSummaries, processed)
	if err != nil {
		log.Printf("final synth error: %v", err)
		// fallback to raw bullets
		var fb strings.Builder
		date := time.Now().Format("2006-01-02")
		fb.WriteString("<html><body><h1>Weekly Digest (Fallback) ")
		fb.WriteString(date)
		fb.WriteString("</h1><pre>")
		fb.WriteString(htmlEscape(strings.Join(perSummaries, Separator)))
		fb.WriteString("</pre></body></html>")
		finalHTML = fb.String()
	}

	if v := validateOutput(finalHTML); v != "" {
		log.Printf("validator: %s", v)
	}

	// Append verification + optional sample
	finalHTML += verifyFooter(processed)
	if strings.ToLower(getenv("APPEND_SAMPLE", "true")) == "true" {
		finalHTML = appendPerEmailSample(finalHTML, perSummaries)
	}

	subject := "Weekly Digest - " + time.Now().Format("2006-01-02")
	if err := gmailSendHTML(ctx, svc, toEmail, subject, finalHTML); err != nil {
		log.Fatalf("send email: %v", err)
	}

	if strings.ToLower(getenv("DRY_RUN", "false")) != "true" {
		var ids []string
		for _, it := range processed {
			ids = append(ids, it.ID)
		}
		_ = svc.Users.Messages.BatchModify("me", &gmail.BatchModifyMessagesRequest{
			Ids:            ids,
			RemoveLabelIds: []string{"UNREAD"},
		}).Do()
	}

	log.Printf("[digest] %s processed=%d sent_to=%s dry_run=%v",
		time.Now().Format(time.RFC3339), len(processed), toEmail, strings.ToLower(getenv("DRY_RUN", "false")) == "true")
}
