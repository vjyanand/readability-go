package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	nurl "net/url"

	readability "codeberg.org/readeck/go-readability/v2"

	"github.com/andybalholm/brotli"
	"github.com/gorilla/handlers"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

type Response struct {
	Title       string `json:"title,omitempty"`
	Body        string `json:"body,omitempty"`
	Image       string `json:"image,omitempty"`
	Url         string `json:"url"`
	Description string `json:"description,omitempty"`
	Uri         string `json:"uri"`
	PubDate     string `json:"created_on,omitempty"`
	Author      string `json:"author,omitempty"`
}

func extruct(w http.ResponseWriter, req *http.Request) {
	url := req.FormValue("url")
	html := req.FormValue("html")
	log.Println(url)
	parsedURL, err := nurl.ParseRequestURI(url)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	reader := strings.NewReader(html)
	article, err := readability.FromReader(reader, parsedURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	
	pubDate := ""
	if pubTime, err := article.PublishedTime(); err == nil {
		pubDate = pubTime.Format("20060102150405")
	} else if modTime, err := article.ModifiedTime(); err == nil {
		pubDate = modTime.Format("20060102150405")
	}

	result := Response{
		Title:       article.Title(),
		Url:         url,
		Image:       article.ImageURL(),
		Uri:         parsedURL.Host,
		Description: article.Excerpt(),
		Author:      article.Byline(),
		PubDate:     pubDate,
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		log.Printf("JSON marshal error: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(jsonBytes))
}

func r(w http.ResponseWriter, req *http.Request) {
	url := req.URL.Query().Get("url")
	if len(url) < 5 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 - url is invalid"))
		return
	}

	log.Println(url)

	parsedURL, err := nurl.ParseRequestURI(url)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	// Try HTTP/3 first, fallback to HTTP/2, then HTTP/1.1
	resp, err := fetchWithHTTP3(url)
	if err != nil {
		resp, err = fetchWithHTTP2(url)
		if err != nil {
			resp, err = fetchWithHTTP1(url)
			if err != nil {
				http.Error(w, "Failed to fetch: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	defer resp.Body.Close()

	article, err := readability.FromReader(resp.Body, parsedURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	pubDate := ""
	if pubTime, err := article.PublishedTime(); err == nil {
		pubDate = pubTime.Format("20060102150405")
	} else if modTime, err := article.ModifiedTime(); err == nil {
		pubDate = modTime.Format("20060102150405")
	}

	var buf bytes.Buffer
	if err := article.RenderHTML(&buf); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		log.Printf("JSON marshal error: %v", err)
		return
	}
	
	result := Response{
		Title:       article.Title(),
		Body:        buf.String(),
		Url:         url,
		Image:       article.ImageURL(),
		Uri:         parsedURL.Host,
		Description: article.Excerpt(),
		Author:      article.Byline(),
		PubDate:     pubDate,
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		log.Printf("JSON marshal error: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		log.Printf("json encode error: %v", err)
	}

	w.Write(jsonBytes)
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "Missing 'url' parameter", http.StatusBadRequest)
		return
	}

	// Validate URL
	_, err := nurl.Parse(targetURL)
	if err != nil {
		http.Error(w, "Invalid URL: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Try HTTP/3 first, fallback to HTTP/2, then HTTP/1.1
	resp, err := fetchWithHTTP3(targetURL)
	if err != nil {
		resp, err = fetchWithHTTP2(targetURL)
		if err != nil {
			resp, err = fetchWithHTTP1(targetURL)
			if err != nil {
				http.Error(w, "Failed to fetch: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Set the status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("Error copying response body: %v\n", err)
	}
}

func fetchWithHTTP3(targetURL string) (*http.Response, error) {
	client := &http.Client{
		Timeout: time.Duration(10) * time.Second,
		Transport: &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
			},
		},
	}
	return makeRequest(client, targetURL)
}

func fetchWithHTTP2(targetURL string) (*http.Response, error) {
	tr := &http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
		},
	}

	client := &http.Client{
		Timeout:   time.Duration(10) * time.Second,
		Transport: tr,
	}

	return makeRequest(client, targetURL)
}

func fetchWithHTTP1(targetURL string) (*http.Response, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
		},
	}

	client := &http.Client{
		Timeout:   time.Duration(10) * time.Second,
		Transport: tr,
	}

	return makeRequest(client, targetURL)
}

func makeRequest(client *http.Client, targetURL string) (*http.Response, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header = http.Header{
		"User-Agent":      []string{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.3 Safari/605.1.15"},
		"Referer":         []string{"https://google.com"},
		"Accept":          []string{"text/html,application/xhtml+xml,application/xml;q=0.9,application/rss+xml,application/json,*/*;q=0.8"},
		"Accept-Encoding": []string{"gzip, br"},
		"Accept-Language": []string{"en-US,en;q=0.9"},
		"Connection":      []string{"keep-alive"},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Save the original body for closing later
	originalBody := resp.Body

	// Decompress based on Content-Encoding
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "gzip":
		gz, err := gzip.NewReader(originalBody)
		if err != nil {
			originalBody.Close()
			return nil, err
		}
		resp.Body = struct {
			io.Reader
			io.Closer
		}{gz, closeFunc(func() error {
			// Close gzip reader first (releases internal resources)
			gz.Close()
			// Then close original connection
			return originalBody.Close()
		})}

	case "deflate":
		// flate.NewReader does NOT return a Closer that closes the underlying stream
		fr := flate.NewReader(originalBody)
		resp.Body = struct {
			io.Reader
			io.Closer
		}{fr, closeFunc(func() error {
			fr.Close() // Important: releases zlib resources
			return originalBody.Close()
		})}

	case "br", "brotli":
		brReader := brotli.NewReader(originalBody)
		resp.Body = brotliReadCloser{
			Reader: brReader,
			closer: originalBody,
		}

	default:
		// No compression: just ensure original body is closed later
		resp.Body = originalBody // will be closed by defer
	}

	// Remove header so downstream doesn't think it's still compressed
	resp.Header.Del("Content-Encoding")

	return resp, nil
}

// Helper type for custom close behavior
type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func check(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

type brotliReadCloser struct {
	*brotli.Reader
	closer io.Closer // the underlying response body
}

func (brc brotliReadCloser) Close() error {
	// First close the brotli reader (resets internal state)
	brc.Reader.Reset(nil)
	// Then close the original body
	return brc.closer.Close()
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/readability", r)
	mux.HandleFunc("/extruct", extruct)
	mux.HandleFunc("/proxy", proxyHandler)
	mux.HandleFunc("/check", check)

	proxiedMux := handlers.ProxyHeaders(mux)

	loggedHandler := handlers.LoggingHandler(os.Stdout, proxiedMux)
	log.Println("Server starting on :8080")
	if err := http.ListenAndServe(":8080", loggedHandler); err != nil {
		log.Fatal("Failed to start:", err)
	}
}
