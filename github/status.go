package github

import "github.com/crosbymichael/octokat"

func (g GitHub) SuccessStatus(repo octokat.Repo, sha, context, description string, targetURL string) error {
	_, err := g.Client().SetStatus(repo, sha, &octokat.StatusOptions{
		State:       "success",
		Context:     context,
		Description: description,
		URL:         targetURL,
	})
	return err
}

func (g GitHub) FailureStatus(repo octokat.Repo, sha, context, description, targetURL string) error {
	_, err := g.Client().SetStatus(repo, sha, &octokat.StatusOptions{
		State:       "failure",
		Context:     context,
		Description: description,
		URL:         targetURL,
	})
	return err
}

func (g GitHub) PendingStatus(repo octokat.Repo, sha, context, description, targetURL string) error {
	_, err := g.Client().SetStatus(repo, sha, &octokat.StatusOptions{
		State:       "pending",
		Context:     context,
		Description: description,
		URL:         targetURL,
	})
	return err
}