package models

type Newsletter struct {
	ID      string
	Subject string
	From    string
	Date    string
	Text    string
	Links   []string
}
