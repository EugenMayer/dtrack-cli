// Package api is a thin client for the Dependency-Track 5.x REST API (v1).
//
// Only the endpoints needed by the current CLI commands are implemented. The
// client targets the v5 API contract, where notably:
//
//   - A project's children are fetched via GET /v1/project/{uuid}/children
//     (the inline "children" array was removed from the project payload in v5).
//   - List endpoints are paginated and cap at 100 results by default. The total
//     count is returned in the X-Total-Count response header.
//   - Collection ("collection project") parents are identified by a non-empty
//     collectionLogic that is not "NONE".
//   - GET /v1/project's "name" filter is an exact match (server-side it's
//     built as "name == :name", not a LIKE/substring filter).
//   - PUT /v1/project/clone processes the clone asynchronously and returns a
//     tracking token immediately, not the finished project.
//   - Bulk deletion is done through POST /v1/project/batchDelete.
//   - A project's "active" flag is toggled via a partial update,
//     PATCH /v1/project/{uuid}. There is no bulk endpoint for this, so
//     deactivating multiple projects means one PATCH per project.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Project is a subset of the Dependency-Track "Project" schema.
type Project struct {
	UUID            string          `json:"uuid"`
	Name            string          `json:"name"`
	Version         string          `json:"version,omitempty"`
	Classifier      string          `json:"classifier,omitempty"`
	CollectionLogic string          `json:"collectionLogic,omitempty"`
	Active          bool            `json:"active"`
	Raw             json.RawMessage `json:"-"`
}

// IsCollection reports whether this project acts as a collection
// (aggregating) parent. In v5, null and the removed "NONE" enum value are
// treated identically.
func (p Project) IsCollection() bool {
	logic := strings.TrimSpace(p.CollectionLogic)
	return logic != "" && logic != "NONE"
}

// Label returns a human-readable "name version" identifier.
func (p Project) Label() string {
	if p.Version != "" {
		return p.Name + " " + p.Version
	}
	return p.Name
}

// Client is a minimal REST client for Dependency-Track.
type Client struct {
	baseURL  string // always ends in "/api/"
	apiKey   string
	pageSize int
	http     *http.Client
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying *http.Client (e.g. to change TLS
// verification or timeouts).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithPageSize sets the pagination page size (default 100).
func WithPageSize(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.pageSize = n
		}
	}
}

// New builds a Client. baseURL may be given with or without a trailing "/api";
// the client normalises it so requests resolve correctly.
func New(baseURL, apiKey string, opts ...Option) *Client {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/api") {
		base += "/api"
	}
	c := &Client{
		baseURL:  base + "/",
		apiKey:   apiKey,
		pageSize: 100,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) resolve(path string) string {
	return c.baseURL + strings.TrimLeft(path, "/")
}

// do performs a request and returns the response for callers that need headers
// (e.g. pagination). The caller must close resp.Body. A non-2xx status is
// converted into an error, with the body drained and closed first.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	full := c.resolve(path)
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, full, reader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		msg := strings.TrimSpace(string(detail))
		if msg != "" {
			return nil, fmt.Errorf("%s %s returned HTTP %d: %s", method, path, resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("%s %s returned HTTP %d", method, path, resp.StatusCode)
	}
	return resp, nil
}

// paginate walks every page of a paginated list endpoint, decoding each page
// into a slice of Project and invoking yield for each item. It stops when the
// server reports (via X-Total-Count) that all items have been seen, or when a
// short page is returned.
func (c *Client) paginate(ctx context.Context, path string, query url.Values, yield func(Project)) error {
	if query == nil {
		query = url.Values{}
	}
	query.Set("pageSize", strconv.Itoa(c.pageSize))

	page := 1
	seen := 0
	for {
		query.Set("pageNumber", strconv.Itoa(page))
		resp, err := c.do(ctx, http.MethodGet, path, query, nil)
		if err != nil {
			return err
		}

		var batch []json.RawMessage
		if derr := json.NewDecoder(resp.Body).Decode(&batch); derr != nil {
			resp.Body.Close()
			return fmt.Errorf("decoding %s page %d: %w", path, page, derr)
		}
		total := resp.Header.Get("X-Total-Count")
		resp.Body.Close()

		if len(batch) == 0 {
			return nil
		}
		for _, raw := range batch {
			var p Project
			if uerr := json.Unmarshal(raw, &p); uerr != nil {
				return fmt.Errorf("decoding project in %s: %w", path, uerr)
			}
			p.Raw = raw
			yield(p)
		}
		seen += len(batch)

		if total != "" {
			if t, perr := strconv.Atoi(total); perr == nil && seen >= t {
				return nil
			}
		} else if len(batch) < c.pageSize {
			return nil
		}
		page++
	}
}

// Version returns the raw server version payload and validates connectivity.
func (c *Client) Version(ctx context.Context) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, "version", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if derr := json.NewDecoder(resp.Body).Decode(&out); derr != nil {
		return nil, fmt.Errorf("decoding version: %w", derr)
	}
	return out, nil
}

// ListProjects returns all projects, optionally excluding inactive ones and/or
// restricting to root projects.
func (c *Client) ListProjects(ctx context.Context, excludeInactive, onlyRoot bool) ([]Project, error) {
	q := url.Values{}
	if excludeInactive {
		q.Set("excludeInactive", "true")
	}
	if onlyRoot {
		q.Set("onlyRoot", "true")
	}
	var out []Project
	err := c.paginate(ctx, "v1/project", q, func(p Project) { out = append(out, p) })
	return out, err
}

// ListProjectsByName returns every version of the project with the exact
// given name, optionally excluding inactive ones. Dependency-Track's "name"
// filter on GET /v1/project is an exact match, not a substring search.
func (c *Client) ListProjectsByName(ctx context.Context, name string, excludeInactive bool) ([]Project, error) {
	q := url.Values{}
	q.Set("name", name)
	if excludeInactive {
		q.Set("excludeInactive", "true")
	}
	var out []Project
	err := c.paginate(ctx, "v1/project", q, func(p Project) { out = append(out, p) })
	return out, err
}

// ListCollectionProjects returns all projects that act as collection parents.
func (c *Client) ListCollectionProjects(ctx context.Context, excludeInactive bool) ([]Project, error) {
	all, err := c.ListProjects(ctx, excludeInactive, false)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(all))
	for _, p := range all {
		if p.IsCollection() {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListChildren returns the direct children of a project. In v5 this is a
// dedicated endpoint; the inline "children" array was removed from the project
// payload.
func (c *Client) ListChildren(ctx context.Context, uuid string, excludeInactive bool) ([]Project, error) {
	q := url.Values{}
	if excludeInactive {
		q.Set("excludeInactive", "true")
	}
	var out []Project
	err := c.paginate(ctx, "v1/project/"+uuid+"/children", q, func(p Project) { out = append(out, p) })
	return out, err
}

// BatchDelete deletes multiple projects in one request, falling back to
// per-project deletes if the batch endpoint is unavailable on the server.
//
// The Dependency-Track endpoint (POST /v1/project/batchDelete) deserializes
// its body directly into a Set<UUID>, so the payload is a bare JSON array of
// UUID strings (["uuid1", "uuid2"]) — not an object wrapping them.
func (c *Client) BatchDelete(ctx context.Context, uuids []string) error {
	if len(uuids) == 0 {
		return nil
	}

	resp, err := c.do(ctx, http.MethodPost, "v1/project/batchDelete", nil, uuids)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "HTTP 405") {
			for _, u := range uuids {
				if derr := c.DeleteProject(ctx, u); derr != nil {
					return derr
				}
			}
			return nil
		}
		return err
	}
	resp.Body.Close()
	return nil
}

// DeleteProject deletes a single project by UUID.
func (c *Client) DeleteProject(ctx context.Context, uuid string) error {
	resp, err := c.do(ctx, http.MethodDelete, "v1/project/"+uuid, nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// DeactivateProject sets a single project's "active" flag to false via a
// partial update (PATCH /v1/project/{uuid}). Dependency-Track merges only the
// fields present in the body, so a bare {"active": false} payload is enough.
func (c *Client) DeactivateProject(ctx context.Context, uuid string) error {
	resp, err := c.do(ctx, http.MethodPatch, "v1/project/"+uuid, nil, map[string]bool{"active": false})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// BatchDeactivate deactivates multiple projects. Dependency-Track has no bulk
// endpoint for toggling "active" (unlike batchDelete), so each project is
// updated individually.
func (c *Client) BatchDeactivate(ctx context.Context, uuids []string) error {
	for _, u := range uuids {
		if err := c.DeactivateProject(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// CloneOptions selects which related data is copied when cloning a project.
// Each field mirrors an "include*" flag accepted by PUT /v1/project/clone;
// all default to false (an empty clone) unless set.
type CloneOptions struct {
	IncludeTags             bool
	IncludeProperties       bool
	IncludeDependencies     bool
	IncludeComponents       bool
	IncludeServices         bool
	IncludeAuditHistory     bool
	IncludeACL              bool
	IncludePolicyViolations bool
	MakeCloneLatest         bool
}

// cloneProjectRequest is the PUT /v1/project/clone payload.
type cloneProjectRequest struct {
	Project                 string `json:"project"`
	Version                 string `json:"version"`
	IncludeTags             bool   `json:"includeTags"`
	IncludeProperties       bool   `json:"includeProperties"`
	IncludeDependencies     bool   `json:"includeDependencies"`
	IncludeComponents       bool   `json:"includeComponents"`
	IncludeServices         bool   `json:"includeServices"`
	IncludeAuditHistory     bool   `json:"includeAuditHistory"`
	IncludeACL              bool   `json:"includeACL"`
	IncludePolicyViolations bool   `json:"includePolicyViolations"`
	MakeCloneLatest         bool   `json:"makeCloneLatest"`
}

// CloneProject clones sourceUUID into a new project at newVersion, copying
// the related data selected by opts. Dependency-Track processes cloning
// asynchronously (it dispatches a background event and returns immediately):
// the returned token identifies that job, not the finished project, and
// there is currently no polling helper for it in this client.
func (c *Client) CloneProject(ctx context.Context, sourceUUID, newVersion string, opts CloneOptions) (string, error) {
	req := cloneProjectRequest{
		Project:                 sourceUUID,
		Version:                 newVersion,
		IncludeTags:             opts.IncludeTags,
		IncludeProperties:       opts.IncludeProperties,
		IncludeDependencies:     opts.IncludeDependencies,
		IncludeComponents:       opts.IncludeComponents,
		IncludeServices:         opts.IncludeServices,
		IncludeAuditHistory:     opts.IncludeAuditHistory,
		IncludeACL:              opts.IncludeACL,
		IncludePolicyViolations: opts.IncludePolicyViolations,
		MakeCloneLatest:         opts.MakeCloneLatest,
	}

	resp, err := c.do(ctx, http.MethodPut, "v1/project/clone", nil, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Token string `json:"token"`
	}
	if derr := json.NewDecoder(resp.Body).Decode(&out); derr != nil {
		return "", fmt.Errorf("decoding clone response: %w", derr)
	}
	return out.Token, nil
}
