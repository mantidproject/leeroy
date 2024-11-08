package jenkins

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"

	log "github.com/sirupsen/logrus"
)

type Client struct {
	Baseurl  string `json:"base_url"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

type JenkinsResponse struct {
	Name  string       `json:"name"`
	Build JenkinsBuild `json:"build"`
}

type JenkinsBuild struct {
	Number     int                    `json:"number"`
	Url        string                 `json:"full_url"`
	Phase      string                 `json:"phase"`
	Status     string                 `json:"status"`
	Parameters JenkinsBuildParameters `json:"parameters"`
}

type JenkinsBuildParameters struct {
	GitBaseRepo string `json:"GIT_BASE_REPO"`
	GitHeadRepo string `json:"GIT_HEAD_REPO"`
	GitSha      string `json:"GIT_SHA1"`
	PR          string `json:"PR"`
}

type Request struct {
	Parameters []map[string]string `json:"parameter"`
}

type JobInstance struct {
	Number int `xml:"build>number"`
}

// Sets the authentication for the Jenkins client
// Password can be an API token as described in:
// https://wiki.jenkins-ci.org/display/JENKINS/Authenticating+scripted+clients
func New(uri, username, token string) *Client {
	return &Client{
		Baseurl:  uri,
		Username: username,
		Token:    token,
	}
}

func (c *Client) Build(job string, data Request) error {
	// encode the request data
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}

	// set up the request
	url := fmt.Sprintf("%s/job/%s/build", c.Baseurl, job)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(d))
	if err != nil {
		return err
	}

	// add the auth
	req.SetBasicAuth(c.Username, c.Token)

	// do the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	// check the status code
	// it should be 201
	if resp.StatusCode != 201 {
		return fmt.Errorf("jenkins post to %s responded with status %d, data: %s", url, resp.StatusCode, string(d))
	}

	return nil
}

func (c *Client) BuildWithParameters(job string, parameters string) error {
	// set up the request
	url := fmt.Sprintf("%s/job/%s/buildWithParameters?%s", c.Baseurl, job, parameters)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte{}))
	if err != nil {
		return err
	}

	// add the auth
	req.SetBasicAuth(c.Username, c.Token)

	// do the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	// check the status code
	// it should be 201
	if resp.StatusCode != 201 {
		return fmt.Errorf("jenkins post to %s responded with status %d", url, resp.StatusCode)
	}

	return nil
}

func (c *Client) GetJobInstance(job string, pr_number int) (int, error) {
	// set up the request
	url := fmt.Sprintf("%s/job/%s/api/xml?tree=builds[number,result,actions[parameters[name,value]]]&xpath=/*/build[action/parameter[name=\"PR\"][value=\"%v\"]][not(result)]&wrapper=found_jobs", c.Baseurl, job, pr_number)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	// do the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}

	reqDump, err := httputil.DumpRequestOut(req, true)
	fmt.Printf("REQUEST:\n%s", string(reqDump))

	// check the status code
	// it should be 200
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("jenkins get to %s responded with status %d", url, resp.StatusCode)
	}

	// read then parse response for job id
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var jobInstance = &JobInstance{}
	if err := xml.Unmarshal(body, &jobInstance); err != nil {
		return 0, err
	}

	return jobInstance.Number, nil
}

func (c *Client) CancelJobInstance(job string, job_id int) error {
	log.Infof("cancelling job instance, job: %s, job number: %v", job, job_id)

	// set up the request
	url := fmt.Sprintf("%s/job/%s/%v/stop", c.Baseurl, job, job_id)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte{}))
	if err != nil {
		return err
	}

	// add the auth
	req.SetBasicAuth(c.Username, c.Token)

	// do the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	// check the status code
	// it should be 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("jenkins post to %s responded with status %d", url, resp.StatusCode)
	}

	return nil
}
