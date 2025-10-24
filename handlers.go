package main

import (
	"encoding/json"
	"fmt"
	"io"
	"leeroy/github"
	"leeroy/jenkins"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crosbymichael/octokat"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

func pingHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "pong")
}

func jenkinsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		log.Errorf("%q is not a valid method", r.Method)
		w.WriteHeader(405)
		return
	}

	// decode the body
	decoder := json.NewDecoder(r.Body)
	var j jenkins.JenkinsResponse
	if err := decoder.Decode(&j); err != nil {
		log.Errorf("decoding the jenkins request as json failed: %v", err)
		return
	}

	log.Infof("Received Jenkins notification for %s %d (%s): %s", j.Name, j.Build.Number, j.Build.Url, j.Build.Phase)

	// if the phase is not started or completed
	// we don't care
	if j.Build.Phase != "STARTED" && j.Build.Phase != "COMPLETED" {
		return
	}

	// get the status for github
	// and create a status description
	desc := fmt.Sprintf("Jenkins build %s %d", j.Name, j.Build.Number)
	var state string
	if j.Build.Phase == "STARTED" {
		state = "pending"
		desc += " is running"
		j.Build.Url += "console"
	} else {
		switch j.Build.Status {
		case "SUCCESS":
			state = "success"
			desc += " has succeeded"
		case "FAILURE":
			state = "failure"
			desc += " has failed"
		case "UNSTABLE":
			state = "failure"
			desc += " was unstable"
		case "ABORTED":
			state = "error"
			desc += " has encountered an error"
		default:
			log.Errorf("Did not understand %q build status. Aborting.", j.Build.Status)
			return
		}
	}

	// get the build
	build, err := config.getBuildByJob(j.Name)
	if err != nil {
		log.Error(err)
		return
	}

	// update the github status
	if err := config.updateGithubStatus(j.Build.Parameters.GitBaseRepo, build.Context, j.Build.Parameters.GitSha, state, desc, j.Build.Url); err != nil {
		log.Errorf("config.updateGithubStatus error %v", err)
	}

	if state == "success" {
		for _, DownstreamBuildContext := range build.DownstreamBuilds {
			DownstreamBuild, err := config.getBuildByContextAndRepo(DownstreamBuildContext, j.Build.Parameters.GitBaseRepo)
			if err != nil {
				log.Error(err)
				return
			}
			skip_build := false
			for _, excludeTarget := range DownstreamBuild.ExcludeTargets {
				if excludeTarget == j.Build.Parameters.BaseBranch {
					log.Infof("Skipping build due to excluded target: %s", excludeTarget)
					skip_build = true
					break
				}
			}

			if !skip_build {
				pr_number, _ := strconv.Atoi(j.Build.Parameters.PR)
				if err := config.scheduleJenkinsDownstreamBuild(DownstreamBuild.Repo, j.Build.Parameters.GitHeadRepo, pr_number, DownstreamBuild, j.Build.Parameters.GitSha, j.Build.Parameters.BaseBranch); err != nil {
					log.Error(err)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}
		}
	}
}

func githubHandler(w http.ResponseWriter, r *http.Request) {
	event := r.Header.Get("X-GitHub-Event")
	switch event {
	case "":
		log.Error("Got GitHub notification without a type")
		return
	case "ping":
		w.WriteHeader(http.StatusOK)
		return
	case "pull_request":
		log.Debugf("Got a pull request hook")
	case "pull_request_review":
		log.Debugf("Got a pull_request_review hook")
		reviewBody, reviewErr := io.ReadAll(r.Body)
		if reviewErr != nil {
			log.Errorf("Error reading github handler body: %v", reviewErr)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		handlePullRequestReview(w, reviewBody)
		return
	default:
		log.Errorf("Got unknown GitHub notification event type: %s", event)
		return
	}

	// parse the pull request
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Errorf("Error reading github handler body: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	prHook, err := octokat.ParsePullRequestHook(body)
	if err != nil {
		log.Errorf("Error parsing hook: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	pr := prHook.PullRequest
	baseRepo := fmt.Sprintf("%s/%s", pr.Base.Repo.Owner.Login, pr.Base.Repo.Name)

	log.Infof("Received GitHub pull request notification for %s %d (%s): %s", baseRepo, pr.Number, pr.URL, prHook.Action)

	// ignore everything we don't care about
	if prHook.Action != "opened" && prHook.Action != "reopened" && prHook.Action != "synchronize" {
		log.Debugf("Ignoring PR hook action %q", prHook.Action)
		return
	}

	g := github.GitHub{
		AuthToken: config.GHToken,
		User:      config.GHUser,
	}

	attempt, totalAttempts := 1, 5
	delay := time.Second
retry:
	pullRequest, err := g.LoadPullRequest(prHook)
	if err != nil {
		log.Errorf("Error loading the pull request (attempt %d/%d): %v", attempt, totalAttempts, err)
		if attempt <= totalAttempts && errors.Cause(err).Error() == "Not Found" {
			time.Sleep(delay)
			attempt++
			delay *= 2
			goto retry
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	mergeable, err := g.IsMergeable(pullRequest)
	if err != nil {
		log.Errorf("Error checking if PR is mergeable: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// PR is not mergeable, so don't start the build
	if !mergeable {
		log.Errorf("Unmergeable PR for %s #%d. Aborting build", baseRepo, pr.Number)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Author is not authorized, so don't start the build
	if !checkIsAuthorizedPRAuthor(prHook, pullRequest, g) {
		getBuildsWhileCancellingExisting(baseRepo, prHook.Number)
		w.WriteHeader(http.StatusOK)
		return
	}

	startJenkinsBuilds(w, baseRepo, pr.Number)
}

// Initiate the jenkins builds after cancelling any existing builds
func startJenkinsBuilds(w http.ResponseWriter, baseRepo string, prNumber int) {
	builds, err := getBuildsWhileCancellingExisting(baseRepo, prNumber)
	if err != nil {
		log.Error(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// schedule the Jenkins builds
	for _, build := range builds {
		if !build.Downstream {
			if err := config.scheduleJenkinsBuild(baseRepo, prNumber, build); err != nil {
				log.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
	}
}

func getBuildsWhileCancellingExisting(baseRepo string, prNumber int) ([]Build, error) {
	// get the builds
	builds, err := config.getBuilds(baseRepo, false)
	if err != nil {
		log.Error(err)
		return nil, err
	}

	// cancel existing Jenkins builds associated with the job
	for _, build := range builds {
		if err := config.cancelJenkinsBuild(baseRepo, prNumber, build); err != nil {
			log.Error(err)
			return nil, err
		}
	}

	return builds, nil
}

// Check if the PR author is valid
func checkIsAuthorizedPRAuthor(prHook *octokat.PullRequestHook, pullRequest *github.PullRequest, g github.GitHub) bool {
	pr := prHook.PullRequest

	if memberValidity, err := isValidMember(pr.User.Login); !memberValidity {
		log.Errorf("Aborting! PR author %s is not an approved user! %v", pr.User.Login, err)

		// Add a comment to the PR
		comment := "Thanks for your submission, " + pr.User.Login + ". Tests can only be initiated by an authorized member of the Mantid team. Please contact us for assistance."
		commentType := "Unapproved user"
		if err := g.AddUniqueComment(pullRequest.Repo, strconv.Itoa(prHook.Number), comment, commentType, pullRequest.Content); err != nil {
			log.Errorf("Failed to add a unique comment '%s': %v", comment, err)
			return false
		}

		prRepoURL := fmt.Sprintf("https://github.com/%s/%s/pulls/", config.OrgName, config.BaseRepoName)

		// Set the PR status as Failed
		if err := g.FailureStatus(pullRequest.Repo, pr.Head.Sha, "mantid/unauthorized",
			"Please contact the mantid team to run tests", prRepoURL); err != nil {
			log.Errorf("Failed to set failed status: %v", err)
			return false
		}

		log.Debugf("Successfully set PR status as failed for %s, %s", pullRequest.Repo.Name, pullRequest.Repo.UserName)
		return false
	}
	return true
}

// Check whether the author of the PR is registered in a git hub team
func isValidMember(author string) (bool, error) {
	if len(config.Teams) == 0 {
		log.Error("github_teams are not defined at config.json!")
		return true, nil
	}

	for _, teamName := range config.Teams {
		url := fmt.Sprintf("https://api.github.com/orgs/%s/teams/%s/memberships/%s", config.OrgName, teamName, author)
		client := &http.Client{}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Errorf("Error creating request: %v", err)
			return false, err
		}
		req.Header.Add("Authorization", "Bearer "+config.GHToken)
		resp, err := client.Do(req)
		if err != nil {
			log.Errorf("Error sending request: %v", err)
			return false, err
		}

		if resp.StatusCode == http.StatusOK {
			return true, nil
		}
	}

	return false, nil
}

type requestBuild struct {
	Number  int    `json:"number"`
	Repo    string `json:"repo"`
	Context string `json:"context"`
}

func customBuildHandler(w http.ResponseWriter, r *http.Request) {
	// setup auth
	user, pass, ok := r.BasicAuth()
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if user != config.User && pass != config.Pass {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != "POST" {
		log.Errorf("%q is not a valid method", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// decode the body
	decoder := json.NewDecoder(r.Body)
	var b requestBuild
	if err := decoder.Decode(&b); err != nil {
		log.Errorf("decoding the retry request as json failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// get the build
	build, err := config.getBuildByContextAndRepo(b.Context, b.Repo)
	if err != nil {
		log.Error(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// schedule the jenkins build
	if err := config.scheduleJenkinsBuild(b.Repo, b.Number, build); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Error(err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func cronBuildHandler(w http.ResponseWriter, r *http.Request) {
	// setup auth
	user, pass, ok := r.BasicAuth()
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if user != config.User && pass != config.Pass {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != "POST" {
		log.Errorf("%q is not a valid method", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// decode the body
	decoder := json.NewDecoder(r.Body)
	var b requestBuild
	if err := decoder.Decode(&b); err != nil {
		log.Errorf("decoding the retry request as json failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// get the build
	build, err := config.getBuildByContextAndRepo(b.Context, b.Repo)
	if err != nil {
		log.Error(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// get PRs that have failed for the context
	nums, err := config.getFailedPRs(b.Context, b.Repo)
	if err != nil {
		log.Error(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for _, prNum := range nums {
		// schedule the jenkins build
		if err := config.scheduleJenkinsBuild(b.Repo, prNum, build); err != nil {
			log.Error(err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

type PullRequestReviewHook struct {
	Action string `json:"action"`
	Review struct {
		State string `json:"state"`
		Body  string `json:"body"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"review"`
	PullRequest struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	} `json:"pull_request"`
	Repository octokat.Repository `json:"repository"`
}

func handlePullRequestReview(w http.ResponseWriter, body []byte) {
	var reviewHook PullRequestReviewHook
	if err := json.Unmarshal(body, &reviewHook); err != nil {
		log.Errorf("Error parsing pull_request_review hook: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Only handle submitted reviews
	if reviewHook.Action != "submitted" {
		log.Debugf("Ignoring pull_request_review action=%s", reviewHook.Action)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Check if the body contains "rerun ci" case insensitively
	if strings.EqualFold(strings.TrimSpace(reviewHook.Review.Body), "rerun ci") {
		log.Debugf("PR was reviewed by:%s", reviewHook.Review.User.Login)

		// Check if reviewer is an approved user
		if isValid, _ := isValidMember(reviewHook.Review.User.Login); isValid {
			//set any past authentication check as passed
			setValidationCheckPassed(reviewHook)
			baseRepo := fmt.Sprintf("%s/%s", reviewHook.Repository.Owner.Login, reviewHook.Repository.Name)

			//Upon this approval kick start the full CI back
			startJenkinsBuilds(w, baseRepo, reviewHook.PullRequest.Number)
			w.WriteHeader(http.StatusOK)
			return
		} else {
			log.Warnf("User %s tried to rerun CI but is not authorized",
				reviewHook.Review.User.Login,
			)
		}

		w.WriteHeader(http.StatusForbidden)
		return
	}

	log.Debugf("Review comment did not match [rerun ci], ignoring")
	w.WriteHeader(http.StatusOK)
}

func setValidationCheckPassed(hook PullRequestReviewHook) {
	gh := github.GitHub{
		AuthToken: config.GHToken,
		User:      config.GHUser,
	}

	prNumber := strconv.Itoa(hook.PullRequest.Number)
	repo := octokat.Repo{
		Name:     hook.Repository.Name,
		UserName: hook.Repository.Owner.Login,
	}

	// Fetch the full PR from GitHub
	pr, err := gh.Client().PullRequest(repo, prNumber, nil)
	if err != nil {
		log.Errorf("failed to fetch PR %s from repo %s, %s, %v", prNumber, repo.Name, repo.UserName, err)
		return
	}

	log.Infof("PR head sha=%s, repo name=%s, username=%s", pr.Head.Sha, repo.Name, repo.UserName)

	prRepoURL := fmt.Sprintf("https://github.com/%s/%s/pulls/", config.OrgName, config.BaseRepoName)
	if err := gh.SuccessStatus(repo,
		pr.Head.Sha,
		"mantid/unauthorized",
		"This PR is now Authorized!",
		prRepoURL); err != nil {
		log.Errorf("Failed to set status as Successful for sha=%s context=mantid/unauthorized: %v", pr.Head.Sha, err)
	}
}
