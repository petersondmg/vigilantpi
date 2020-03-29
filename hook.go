package main

import (
	"io/ioutil"
	"net/http"
	"strings"
)

// Hook ...
type Hook struct {
	URL       string `yaml:"url"`
	Method    string `yaml:"method"`
	BasicUser string `yaml:"basic_user"`
	BasicPass string `yaml:"basic_pass"`
	Headers   []struct {
		Name  string `yaml:"name"`
		Value string `yaml:"value"`
	} `yaml:"headers"`
	Expect      string `yaml:"expect"`
	Description string `yaml:"desc"`
}

func (h *Hook) Run() {
	req, err := http.NewRequest(strings.ToUpper(h.Method), h.URL, nil)
	if err != nil {
		logger.Printf("error on hook %s - url: %s: %s", h.Description, h.URL, err)
		return
	}
	for _, header := range h.Headers {
		req.Header.Set(header.Name, header.Value)
	}

	if h.BasicUser != "" || h.BasicPass != "" {
		req.SetBasicAuth(h.BasicUser, h.BasicPass)
	}

	go func() {
		res, err := preRecClient.Do(req)
		if err != nil {
			logger.Printf("error on hook %s request: %s", h.Description, err)
			return
		}
		defer res.Body.Close()
		body, _ := ioutil.ReadAll(res.Body)
		bodyText := string(body)
		if h.Expect != "" && !strings.Contains(bodyText, h.Expect) {
			logger.Printf("hook %s returned unexpected result. expected %s, got %s", h.Description, h.Expect, bodyText)
		}
	}()
}
