package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

var (
	tasksClient = http.Client{
		Timeout: time.Second * 60,
	}

	taskByName map[string]*Task
)

func init() {
	taskByName = make(map[string]*Task)
}

type (
	Task struct {
		Name    string       `yaml:"name"`
		Request *RequestTask `yaml:"request"`
		Command *string      `yaml:"command"`
	}

	Tasks []*Task

	RequestTask struct {
		URL       string `yaml:"url"`
		Method    string `yaml:"method"`
		BasicUser string `yaml:"basic_user"`
		BasicPass string `yaml:"basic_pass"`
		Headers   []struct {
			Name  string `yaml:"name"`
			Value string `yaml:"value"`
		} `yaml:"headers"`
		Expect string `yaml:"expect"`
	}
)

func (ts Tasks) Init() {
	for _, task := range ts {
		if _, exists := taskByName[task.Name]; exists {
			logger.Printf("task %s was previously declared replacing", task.Name)
		}
		taskByName[task.Name] = task
	}
}

func (t *RequestTask) do() error {
	req, err := http.NewRequest(strings.ToUpper(t.Method), t.URL, nil)
	if err != nil {
		return fmt.Errorf("error on url: %s: %s", t.URL, err)
	}
	for _, header := range t.Headers {
		req.Header.Set(header.Name, header.Value)
	}
	if t.BasicUser != "" || t.BasicPass != "" {
		req.SetBasicAuth(t.BasicUser, t.BasicPass)
	}

	res, err := tasksClient.Do(req)
	if err != nil {
		return err
	}

	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	bodyText := string(body)

	if t.Expect != "" && !strings.Contains(bodyText, t.Expect) {
		fmt.Errorf("received unexpected result. expected %s, got %s", t.Expect, bodyText)
	}

	return nil
}

func (t *Task) run() (string, error) {
	if t.Command != nil {
		out, err := exec.Command("bash", "-c", replaceWithConf(*t.Command)).Output()
		if err != nil {
			logger.Printf("error executing command task %s: %s", t.Name, err)
		}
		s := string(out)
		if s != "" {
			logger.Print(s)
		}
		return s, err
	}

	if t.Request != nil {
		if err := t.Request.do(); err != nil {
			logger.Printf("error executing request task %s: %s", t.Name, err)
			return "[error]", err
		}
	}
	return "[done]", nil
}

func (t *Task) Run() {
	go t.run()
}
