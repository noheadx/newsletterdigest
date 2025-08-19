package processor

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"newsletterdigest_go/config"
	"newsletterdigest_go/models"
	"newsletterdigest_go/openai"
	"newsletterdigest_go/utils"
	"newsletterdigest_go/validator"
)

const Separator = "\n\n——————————————————\n\n"

type Processor struct {
	openaiClient *openai.Client
	config       *config.Config
}

func New(client *openai.Client, cfg *config.Config) *Processor {
	return &Processor{
		openaiClient: client,
		config:       cfg,
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
	
	finalHTML += p.createVerificationFooter(processedItems)
	if p.config.AppendSample {
		finalHTML = p.appendPerEmailSample(finalHTML, perSummaries)
	}

	return finalHTML, processedItems, nil
}

func (p *Processor) summarizeSingle(ctx context.Context, newsletter *models.Newsletter) (string, error) {
	body := utils.CleanText(newsletter.Text)
	if len(body) == 0 {
		return "", fmt.Errorf("empty body")
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
		{Role: "system", Content: p.systemPass1()},
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

	system := p.systemFinal()
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

	messages := []openai.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	out, err := p.openaiClient.Chat(ctx, p.config.FinalModel, messages, 0.2, 1700)
	if err != nil {
		return "", err
	}

	out = strings.Replace(out, "```html", "<html", 1)
	out = p.stripCodeFences(out)
	out = p.normalizeHTMLSummary(out)

	// Fallback if model didn't emit HTML at all
	if !strings.Contains(strings.ToLower(out), "<html") {
		date := time.Now().Format("2006-01-02")
		out = p.htmlScaffold(date) + "<p><pre>" + utils.HtmlEscape(out) + "</pre></p>"
		return out, nil
	}

	// Guard: if output looks like a template (mostly "…"), try a simpler re-ask once
	if p.looksLikeEmptyTemplate(out) {
		simple := []openai.ChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: "Skip any template. Output full HTML with real bullets only; omit empty sections.\n\n" + user},
		}
		out2, err2 := p.openaiClient.Chat(ctx, p.config.FinalModel, simple, 0.2, 1700)
		if err2 == nil && strings.Contains(strings.ToLower(out2), "<html") && !p.looksLikeEmptyTemplate(out2) {
			return out2, nil
		}
	}
	
	return out, nil
}

func (p *Processor) systemPass1() string {
	return "You summarize single newsletters for a product executive. " +
		"Return 3–6 short bullets as plain text lines (no HTML/Markdown). " +
		"Priorities: 1) Product Management 2) Healthcare 3) Architecture 4) Team Organization. " +
		"Compress or omit pure AI hype unless it clearly impacts those four. No fluff."
}

func (p *Processor) systemFinal() string {
	return "You assemble a concise weekly digest for a product executive. " +
		"Priorities: 1) Product Management 2) Healthcare 3) Architecture 4) Team Organization. " +
		"AI is lowest priority; include only if clearly relevant. Keep key figures and decisions. " +
		"OUTPUT STRICT, WELL-FORMED HTML ONLY (no Markdown). Use <h1>, <h2>, <ul>, <li>, <p>, <a>. " +
		"When you see [L1], [L2], etc., replace them with proper HTML anchors using the corresponding URLs supplied for that item. " +
		"Do not include Markdown code fences (```html). Output raw HTML only."
}

func (p *Processor) htmlScaffold(date string) string {
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
	var rows []string
	for i, it := range items {
		sample := ""
		if len(it.Links) > 0 {
			sample = fmt.Sprintf(`<a href="%s">link</a>`, it.Links[0])
		}
		rows = append(rows, fmt.Sprintf(
			"<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
			i+1, utils.HtmlEscape(it.Subject), utils.HtmlEscape(it.From), utils.HtmlEscape(it.Date), sample,
		))
	}
	return `<hr/><h2 class="small">Verification</h2><p class="small">Processed ` + fmt.Sprintf("%d", len(items)) +
		` newsletter(s). Order below is input order, not priority.</p>
<table><thead><tr><th>#</th><th>Subject</th><th>From</th><th>Date</th><th>Sample link</th></tr></thead><tbody>` +
		strings.Join(rows, "") + `</tbody></table>`
}

func (p *Processor) appendPerEmailSample(html string, per []string) string {
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
		sb.WriteString(fmt.Sprintf("<h3 class='small'>%s</h3><pre class='small'>%s</pre>", utils.HtmlEscape(title), utils.HtmlEscape(per[i])))
	}
	return html + sb.String()
}

var ellipsisLi = regexp.MustCompile(`(?i)<li>\s*…\s*</li>`)
var anyLi = regexp.MustCompile(`(?i)<li>.*?</li>`)

func (p *Processor) looksLikeEmptyTemplate(html string) bool {
	total := len(anyLi.FindAllString(html, -1))
	empty := len(ellipsisLi.FindAllString(html, -1))
	// Consider it empty if most bullets are ellipses or there are zero real bullets
	return total == 0 || (empty >= total-1 && total > 0)
}

func (p *Processor) normalizeHTMLSummary(html string) string {
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

func (p *Processor) stripCodeFences(s string) string {
	m := codeFence.FindStringSubmatch(s)
	if len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(s)
}
