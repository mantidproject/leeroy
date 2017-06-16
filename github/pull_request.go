package github

import (
	"strconv"
	"strings"

	"github.com/crosbymichael/octokat"
	"github.com/pkg/errors"
)

// PullRequest describes a github pull request
type PullRequest struct {
	Hook    *octokat.PullRequestHook
	Repo    octokat.Repo
	Content *PullRequestContent
	*octokat.PullRequest
}

// LoadPullRequest takes an incoming PullRequestHook and converts it to the PullRequest type
func (g GitHub) LoadPullRequest(hook *octokat.PullRequestHook) (*PullRequest, error) {
	pr := hook.PullRequest
	repo := nameWithOwner(hook.Repo)

	content, err := g.GetContent(repo, hook.Number, true)
	if err != nil {
		return nil, err
	}

	return &PullRequest{
		Hook:        hook,
		Repo:        repo,
		Content:     content,
		PullRequest: pr,
	}, nil
}

// PullRequestContent contains the files, commits, and comments for a given
// pull request
type PullRequestContent struct {
	id       int
	files    []*octokat.PullRequestFile
	commits  []octokat.Commit
	comments []octokat.Comment
}

// HasDocsChanges checks for docs changes.
func (p *PullRequestContent) IsOnlyDocsChanges() bool {
	if len(p.files) == 0 {
		return false
	}

	// Did any files in the docs dir change?
	for _, f := range p.files {
		if !strings.HasPrefix(f.FileName, "docs/") {
			return false
		}
	}

	return true
}

// This can be used to check skipping clang format and
// CPP check
func (p *PullRequestContent) hasCppFiles() bool {
	if len(p.files) == 0 {
		return false
	}

	// if there are any changed files not in docs/man/experimental dirs
	for _, f := range p.files {
		if hasAny(strings.HasSuffix, f.FileName, ".cpp", ".cxx", ".cc", "c++", ".c", ".tpp", ".txx", ".h", ".hpp", ".hxx") {
			return true
		}
	}
	return false
}

func (p *PullRequestContent) containsPythonFiles() bool {
	if len(p.files) == 0 {
		return false
	}

	// if there are any changed files not in docs/man/experimental dirs
	for _, f := range p.files {
		if hasAny(strings.HasSuffix, f.FileName, ".py") {
			return true
		}
	}
	return false
}

// FindComment finds a specific comment.
func (p *PullRequestContent) FindComment(commentType, user string) *octokat.Comment {
	for _, c := range p.comments {
		if strings.ToLower(c.User.Login) == user && strings.Contains(c.Body, commentType) {
			return &c
		}
	}
	return nil
}

// AlreadyCommented checks if the user has already commented.
func (p *PullRequestContent) AlreadyCommented(commentType, user string) bool {
	for _, c := range p.comments {
		// if we already made the comment return nil
		if strings.ToLower(c.User.Login) == user && strings.Contains(c.Body, commentType) {
			return true
		}
	}
	return false
}

// GetContent returns the content of the issue/pull request number passed.
func (g *GitHub) GetContent(repo octokat.Repo, id int, isPR bool) (*PullRequestContent, error) {
	var (
		files    []*octokat.PullRequestFile
		commits  []octokat.Commit
		comments []octokat.Comment
		err      error
	)
	n := strconv.Itoa(id)

	options := &octokat.Options{
		QueryParams: map[string]string{"per_page": "100"},
	}

	if isPR {
		if commits, err = g.Client().Commits(repo, n, options); err != nil {
			return nil, errors.Wrap(err, "commits")
		}

		if files, err = g.Client().PullRequestFiles(repo, n, options); err != nil {
			return nil, errors.Wrap(err, "files")
		}
	}

	if comments, err = g.Client().Comments(repo, n, options); err != nil {
		return nil, errors.Wrap(err, "comments")
	}

	return &PullRequestContent{
		id:       id,
		files:    files,
		commits:  commits,
		comments: comments,
	}, nil
}

func hasAny(fn func(string, string) bool, s string, cases ...string) bool {
	for _, c := range cases {
		if fn(s, c) {
			return true
		}
	}
	return false
}
