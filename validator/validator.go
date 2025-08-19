package validator

import (
	"strings"
)

func ValidateOutput(html string) string {
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
