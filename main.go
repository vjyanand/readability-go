package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	nurl "net/url"

	"github.com/aws/aws-lambda-go/events"
	runtime "github.com/aws/aws-lambda-go/lambda"
	readability "github.com/vjyanand/go-readability"
)

type Response struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Image       string `json:"image"`
	Url         string `json:"url"`
	Description string `json:"description"`
	Uri         string `json:"uri"`
	PubDate     string `json:"created_on"`
}

func handleRequest(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	url, found := req.QueryStringParameters["url"]

	log.Println(url)

	if !found || len(url) < 5 {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusBadRequest, Headers: map[string]string{
			"Content-Type": "application/json",
		}}, nil
	}

	parsedURL, err := nurl.ParseRequestURI(url)
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusBadRequest, Headers: map[string]string{
			"Content-Type": "application/json",
		}}, nil
	}

	tr := &http.Transport{
		DisableKeepAlives:  true,
		MaxIdleConns:       1,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: false,
	}
	client := &http.Client{Timeout: time.Duration(10) * time.Second, Transport: tr}
	reqest, err := http.NewRequest("GET", url, nil)
	reqest.Header = http.Header{
		"User-Agent": []string{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.3 Safari/605.1.15"},
		"Referer":    []string{"https://google.com"},
	}
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError, Headers: map[string]string{
			"Content-Type": "application/json",
		}}, nil
	}
	resp, err := client.Do(reqest)
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError, Headers: map[string]string{
			"Content-Type": "application/json",
		}}, nil
	}
	defer resp.Body.Close()

	article, err := readability.FromReader(resp.Body, parsedURL)

	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError, Headers: map[string]string{
			"Content-Type": "application/json",
		}}, nil
	}

	r := Response{Title: article.Title,
		Body:        article.Content,
		Url:         url,
		Image:       article.Image,
		Uri:         parsedURL.Host,
		Description: article.Excerpt,
		PubDate:     article.PubDate,
	}
	jsonBytes, _ := json.Marshal(r)

	return events.APIGatewayProxyResponse{StatusCode: http.StatusOK, Headers: map[string]string{
		"Content-Type": "application/json; charset=utf-8",
	}, Body: string(jsonBytes)}, nil

}

func main() {
	runtime.Start(handleRequest)
}
