// Author: Emmanuel COLUSSI
// Copyright (c) 2026 Emmanuel COLUSSI
// SPDX-License-Identifier: MIT
//
// Package confluence provides a minimal client for the Confluence Server/Data
// Center REST API v1 (rest/api/content, .../child/attachment).
package confluence

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// defaultUserAgent resembles a recent browser. Many reverse proxies and WAFs
// silently block script-like user agents before requests reach Confluence.
const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// Client communicates with the Confluence Server/Data Center REST API v1.
type Client struct {
	BaseURL    string // e.g. https://confluence.example.com (without /rest/api)
	Token      string // Personal Access Token -> Authorization: Bearer <token>
	Username   string // basic-auth fallback
	Password   string
	UserAgent  string // User-Agent sent with every request; see defaultUserAgent
	HTTPClient *http.Client
}

// New constructs a Client. insecure disables TLS certificate verification for
// internal instances with self-signed certificates.
func New(baseURL, token, username, password string, insecure bool) *Client {
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // Explicitly selected by the user through --insecure.
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		Username:  username,
		Password:  password,
		UserAgent: defaultUserAgent,
		HTTPClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: tr,
		},
	}
}

func (c *Client) authorize(req *http.Request) {
	ua := c.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Atlassian-Token", "no-check")
	// Some WAFs and reverse proxies require matching Origin and Referer headers
	// on state-changing requests as a CSRF protection measure.
	req.Header.Set("Origin", c.BaseURL)
	req.Header.Set("Referer", c.BaseURL+"/")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
}

func (c *Client) apiURL(path string, query url.Values) string {
	u := c.BaseURL + "/rest/api" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (c *Client) doJSON(method, path string, query url.Values, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.apiURL(path, query), reader)
	if err != nil {
		return err
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	c.authorize(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s -> HTTP %d: %s", method, path, resp.StatusCode, truncate(string(respBody), 800))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response %s %s: %w", method, path, err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- API types ----

type links struct {
	WebUI string `json:"webui"`
}

type space struct {
	Key string `json:"key"`
}

type version struct {
	Number int `json:"number"`
}

type storageValue struct {
	Value string `json:"value"`
}

type bodyValue struct {
	Storage storageValue `json:"storage"`
}

// Page represents a Confluence page returned by the API.
type Page struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Version version   `json:"version"`
	Space   space     `json:"space"`
	Body    bodyValue `json:"body"`
	Links   links     `json:"_links"`
}

// StorageValue returns the current storage-format content or an empty string
// when it was not loaded.
func (p *Page) StorageValue() string {
	if p == nil {
		return ""
	}
	return p.Body.Storage.Value
}

// URL builds the human-facing web URL for a page.
func (c *Client) URL(p *Page) string {
	if p == nil {
		return ""
	}
	return c.BaseURL + p.Links.WebUI
}

type searchResponse struct {
	Results []Page `json:"results"`
	Size    int    `json:"size"`
}

// FindPageByTitle searches a space for an exact page title. It returns
// (nil, nil) when no page matches.
func (c *Client) FindPageByTitle(spaceKey, title string) (*Page, error) {
	q := url.Values{}
	q.Set("spaceKey", spaceKey)
	q.Set("title", title)
	q.Set("expand", "version,space,body.storage")

	var res searchResponse
	if err := c.doJSON(http.MethodGet, "/content", q, nil, &res); err != nil {
		return nil, err
	}
	if len(res.Results) == 0 {
		return nil, nil
	}
	return &res.Results[0], nil
}

// GetPage retrieves a page by ID.
func (c *Client) GetPage(id string) (*Page, error) {
	q := url.Values{}
	q.Set("expand", "version,space,body.storage")
	var p Page
	if err := c.doJSON(http.MethodGet, "/content/"+id, q, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ResolveParentID accepts a numeric page ID or a title searched in spaceKey.
func (c *Client) ResolveParentID(spaceKey, parentRef string) (string, error) {
	if parentRef == "" {
		return "", nil
	}
	if _, err := strconv.Atoi(parentRef); err == nil {
		return parentRef, nil
	}
	p, err := c.FindPageByTitle(spaceKey, parentRef)
	if err != nil {
		return "", fmt.Errorf("resolve parent page %q: %w", parentRef, err)
	}
	if p == nil {
		return "", fmt.Errorf("parent page not found in space %s: %q", spaceKey, parentRef)
	}
	return p.ID, nil
}

type storageBody struct {
	Value          string `json:"value"`
	Representation string `json:"representation"`
}

type bodyWrapper struct {
	Storage storageBody `json:"storage"`
}

type ancestor struct {
	ID string `json:"id"`
}

type createPageRequest struct {
	Type      string      `json:"type"`
	Title     string      `json:"title"`
	Space     space       `json:"space"`
	Ancestors []ancestor  `json:"ancestors,omitempty"`
	Body      bodyWrapper `json:"body"`
}

// CreatePage creates a page in spaceKey under the optional parentID.
func (c *Client) CreatePage(spaceKey, title, parentID, storageXHTML string) (*Page, error) {
	req := createPageRequest{
		Type:  "page",
		Title: title,
		Space: space{Key: spaceKey},
		Body:  bodyWrapper{Storage: storageBody{Value: storageXHTML, Representation: "storage"}},
	}
	if parentID != "" {
		req.Ancestors = []ancestor{{ID: parentID}}
	}
	var p Page
	if err := c.doJSON(http.MethodPost, "/content", nil, req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

type updatePageRequest struct {
	Type    string      `json:"type"`
	Title   string      `json:"title"`
	Version version     `json:"version"`
	Body    bodyWrapper `json:"body"`
}

// UpdatePage updates an existing page. currentVersion must be its latest known version.
func (c *Client) UpdatePage(id, title, storageXHTML string, currentVersion int) (*Page, error) {
	req := updatePageRequest{
		Type:    "page",
		Title:   title,
		Version: version{Number: currentVersion + 1},
		Body:    bodyWrapper{Storage: storageBody{Value: storageXHTML, Representation: "storage"}},
	}
	var p Page
	if err := c.doJSON(http.MethodPut, "/content/"+id, nil, req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ---- Attachments ----

type attachmentSearchResponse struct {
	Results []struct {
		ID         string `json:"id"`
		Extensions struct {
			FileSize int64 `json:"fileSize"`
		} `json:"extensions"`
	} `json:"results"`
}

// findAttachment returns an attachment ID and byte size. found is false when
// filename does not exist on pageID.
func (c *Client) findAttachment(pageID, filename string) (id string, size int64, found bool, err error) {
	q := url.Values{}
	q.Set("filename", filename)
	var res attachmentSearchResponse
	if err := c.doJSON(http.MethodGet, "/content/"+pageID+"/child/attachment", q, nil, &res); err != nil {
		return "", 0, false, err
	}
	if len(res.Results) == 0 {
		return "", 0, false, nil
	}
	return res.Results[0].ID, res.Results[0].Extensions.FileSize, true, nil
}

// UploadAttachment uploads localPath as filename on pageID, replacing an
// existing attachment. A file with the same byte size is skipped to avoid
// creating an identical version when no content hash is available.
func (c *Client) UploadAttachment(pageID, filename, localPath string) (skipped bool, err error) {
	fi, err := os.Stat(localPath)
	if err != nil {
		return false, fmt.Errorf("read file %s: %w", localPath, err)
	}

	existingID, existingSize, found, err := c.findAttachment(pageID, filename)
	if err != nil {
		return false, fmt.Errorf("find existing attachment %q: %w", filename, err)
	}
	if found && existingSize == fi.Size() {
		return true, nil
	}

	path := "/content/" + pageID + "/child/attachment"
	if existingID != "" {
		path = "/content/" + pageID + "/child/attachment/" + existingID + "/data"
	}

	f, err := os.Open(localPath)
	if err != nil {
		return false, fmt.Errorf("open file %s: %w", localPath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return false, err
	}
	_ = mw.WriteField("comment", "published by confluence-publish")
	if err := mw.Close(); err != nil {
		return false, err
	}

	req, err := http.NewRequest(http.MethodPost, c.apiURL(path, nil), &buf)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.authorize(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("upload %s: %w", filename, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("upload %s -> HTTP %d: %s", filename, resp.StatusCode, truncate(string(respBody), 800))
	}
	return false, nil
}

// ---- Labels ----

type labelRequest struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
}

// AddLabels adds global labels to pageID.
func (c *Client) AddLabels(pageID string, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	reqs := make([]labelRequest, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		reqs = append(reqs, labelRequest{Prefix: "global", Name: l})
	}
	if len(reqs) == 0 {
		return nil
	}
	return c.doJSON(http.MethodPost, "/content/"+pageID+"/label", nil, reqs, nil)
}
