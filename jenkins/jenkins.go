package jenkins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"encoding/xml"
	"io/ioutil"
	"strconv"
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


func (c *Client) GetJobInstance(job string, pr_number int, sha string) (int, error) {
	// set up the request
	url := fmt.Sprintf("%s/job/%s/api/xml?tree=builds[number,result,actions[parameters[name,value]]]&xpath=/freeStyleProject/build[action/parameter[name=\"PR\"][value=\"%s\"]][action/parameter[name=\"GIT_SHA1\"][value=\"%s\"]][not(result)]&wrapper=found_jobs", c.Baseurl, job, strconv.Itoa(pr_number), sha)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	// add the auth - Not sure this will be needed for GET
	req.SetBasicAuth(c.Username, c.Token)

	// do the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}

	// check the status code
	// it should be 201
	if resp.StatusCode != 201 {
		return 0, fmt.Errorf("jenkins post to %s responded with status %d", url, resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	//parse response for job id
	var jobInstance = &JobInstance{}
	if err := xml.Unmarshal(body, &jobInstance); err != nil {
		panic(err)
	}

	return jobInstance.Number, nil
}

func (c *Client) CancelJobInstance(job string, job_id int) error {
	// set up the request
	url := fmt.Sprintf("%s/job/%s/%s/stop", c.Baseurl, job, job_id)
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
	// it should be 201 - need to check this.
	if resp.StatusCode != 201 {
		return fmt.Errorf("jenkins post to %s responded with status %d", url, resp.StatusCode)
	}

	return nil
}
