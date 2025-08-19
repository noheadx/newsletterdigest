package gmail

import (
	"context"
	"encoding/base64"
	"net/mail"
	"strings"

	"newsletterdigest_go/credentials"
	"newsletterdigest_go/models"
	"newsletterdigest_go/utils"

	gmail "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Service struct {
	svc *gmail.Service
}

func NewService(ctx context.Context) (*Service, error) {
	store, err := credentials.NewStoreFromEnv()
	if err != nil {
		return nil, err
	}

	client, err := store.GetOAuthClient(ctx)
	if err != nil {
		return nil, err
	}

	svc, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}

	return &Service{svc: svc}, nil
}

func (s *Service) FetchNewsletters(ctx context.Context, query string, maxResults int64) ([]*models.Newsletter, error) {
	call := s.svc.Users.Messages.List("me").Q(query).MaxResults(maxResults)
	list, err := call.Do()
	if err != nil {
		return nil, err
	}

	var newsletters []*models.Newsletter
	for _, m := range list.Messages {
		full, err := s.svc.Users.Messages.Get("me", m.Id).Format("full").Do()
		if err != nil {
			continue
		}

		hdr := make(map[string]string)
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

		text, links := utils.PartsToTextAndLinks(full.Payload)
		text = utils.CleanText(text)
		
		if len(text) < 400 {
			continue
		}

		newsletters = append(newsletters, &models.Newsletter{
			ID:      m.Id,
			Subject: subj,
			From:    from,
			Date:    date,
			Text:    text,
			Links:   links,
		})
	}

	return newsletters, nil
}

func (s *Service) SendHTML(ctx context.Context, to, subject, htmlBody string) error {
	var b strings.Builder
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n\r\n")
	b.WriteString("--BOUNDARY\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n")
	b.WriteString(htmlBody + "\r\n")
	b.WriteString("--BOUNDARY--")

	raw := base64.URLEncoding.EncodeToString([]byte(b.String()))
	_, err := s.svc.Users.Messages.Send("me", &gmail.Message{Raw: raw}).Do()
	return err
}

func (s *Service) MarkAsRead(ctx context.Context, newsletters []*models.Newsletter) error {
	var ids []string
	for _, newsletter := range newsletters {
		ids = append(ids, newsletter.ID)
	}
	
	return s.svc.Users.Messages.BatchModify("me", &gmail.BatchModifyMessagesRequest{
		Ids:            ids,
		RemoveLabelIds: []string{"UNREAD"},
	}).Do()
}
