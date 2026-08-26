// Package confluence est un client minimal pour l'API REST v1 de
// Confluence Server / Data Center (rest/api/content, .../child/attachment).
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

// Client parle à l'API REST v1 de Confluence Server/Data Center.
type Client struct {
	BaseURL    string // ex: https://confluence.exemple.com (sans /rest/api)
	Token      string // Personal Access Token -> Authorization: Bearer <token>
	Username   string // fallback auth basique
	Password   string
	HTTPClient *http.Client
}

// New construit un Client. insecure désactive la vérification du
// certificat TLS (utile pour des instances internes en certificat
// auto-signé).
func New(baseURL, token, username, password string, insecure bool) *Client {
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // choix explicite de l'utilisateur via --insecure
	}
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Token:    token,
		Username: username,
		Password: password,
		HTTPClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: tr,
		},
	}
}

func (c *Client) authorize(req *http.Request) {
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
		return fmt.Errorf("requête %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s -> HTTP %d: %s", method, path, resp.StatusCode, truncate(string(respBody), 800))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("décodage réponse %s %s: %w", method, path, err)
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

// ---- Types API ----

type links struct {
	WebUI string `json:"webui"`
}

type space struct {
	Key string `json:"key"`
}

type version struct {
	Number int `json:"number"`
}

// Page représente une page Confluence telle que renvoyée par l'API.
type Page struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Version version `json:"version"`
	Space   space   `json:"space"`
	Links   links   `json:"_links"`
}

// URL construit l'URL humaine (webui) de la page.
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

// FindPageByTitle cherche une page par titre exact dans un espace. Retourne
// (nil, nil) si aucune page ne correspond.
func (c *Client) FindPageByTitle(spaceKey, title string) (*Page, error) {
	q := url.Values{}
	q.Set("spaceKey", spaceKey)
	q.Set("title", title)
	q.Set("expand", "version,space")

	var res searchResponse
	if err := c.doJSON(http.MethodGet, "/content", q, nil, &res); err != nil {
		return nil, err
	}
	if len(res.Results) == 0 {
		return nil, nil
	}
	return &res.Results[0], nil
}

// GetPage récupère une page par id.
func (c *Client) GetPage(id string) (*Page, error) {
	q := url.Values{}
	q.Set("expand", "version,space")
	var p Page
	if err := c.doJSON(http.MethodGet, "/content/"+id, q, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ResolveParentID accepte soit un id numérique de page, soit un titre de
// page (recherché dans spaceKey), et retourne l'id correspondant.
func (c *Client) ResolveParentID(spaceKey, parentRef string) (string, error) {
	if parentRef == "" {
		return "", nil
	}
	if _, err := strconv.Atoi(parentRef); err == nil {
		return parentRef, nil
	}
	p, err := c.FindPageByTitle(spaceKey, parentRef)
	if err != nil {
		return "", fmt.Errorf("résolution de la page parente %q: %w", parentRef, err)
	}
	if p == nil {
		return "", fmt.Errorf("page parente introuvable dans l'espace %s: %q", spaceKey, parentRef)
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

// CreatePage crée une nouvelle page dans spaceKey, sous parentID (optionnel).
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

// UpdatePage met à jour le contenu (et éventuellement le titre) d'une page
// existante. currentVersion doit être la version actuelle connue de la page.
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

// ---- Pièces jointes ----

type attachmentSearchResponse struct {
	Results []struct {
		ID string `json:"id"`
	} `json:"results"`
}

// findAttachmentID retourne l'id de la pièce jointe filename sur la page
// pageID, ou "" si elle n'existe pas encore.
func (c *Client) findAttachmentID(pageID, filename string) (string, error) {
	q := url.Values{}
	q.Set("filename", filename)
	var res attachmentSearchResponse
	if err := c.doJSON(http.MethodGet, "/content/"+pageID+"/child/attachment", q, nil, &res); err != nil {
		return "", err
	}
	if len(res.Results) == 0 {
		return "", nil
	}
	return res.Results[0].ID, nil
}

// UploadAttachment envoie (ou remplace si elle existe déjà) le fichier
// localPath comme pièce jointe filename de la page pageID.
func (c *Client) UploadAttachment(pageID, filename, localPath string) error {
	existingID, err := c.findAttachmentID(pageID, filename)
	if err != nil {
		return fmt.Errorf("recherche pièce jointe existante %q: %w", filename, err)
	}

	path := "/content/" + pageID + "/child/attachment"
	if existingID != "" {
		path = "/content/" + pageID + "/child/attachment/" + existingID + "/data"
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("ouverture fichier %s: %w", localPath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	_ = mw.WriteField("comment", "publié via confluence-publish")
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.apiURL(path, nil), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "nocheck")
	c.authorize(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload %s: %w", filename, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("upload %s -> HTTP %d: %s", filename, resp.StatusCode, truncate(string(respBody), 800))
	}
	return nil
}

// ---- Labels ----

type labelRequest struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
}

// AddLabels ajoute des labels globaux à la page pageID.
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
