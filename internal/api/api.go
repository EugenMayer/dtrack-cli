// Package api is a thin wrapper around the official Dependency-Track Go
// client (github.com/DependencyTrack/client-go), adapting it to the small,
// simplified Project shape and method set this CLI actually needs. It
// targets the v5 API contract, where notably:
//
//   - A project's children are fetched via GET /v1/project/{uuid}/children
//     (the inline "children" array was removed from the project payload in v5).
//   - Collection ("collection project") parents are identified by a non-empty
//     collectionLogic that is not "NONE".
//   - GET /v1/project's "name" filter is an exact match (server-side it's
//     built as "name == :name", not a LIKE/substring filter). The underlying
//     client-go call for this (ProjectService.GetProjectsForName) is not
//     paginated, so a project with more matching versions than one page
//     (100 by default) would be truncated; this is a known, accepted
//     limitation given how unlikely that is in practice.
//   - GET /v1/project/lookup?name=&version= resolves a single project by its
//     exact name+version, 404ing if there is no match.
//   - PUT /v1/project/clone processes the clone asynchronously and returns a
//     tracking token immediately, not the finished project. There is no bulk
//     clone endpoint.
//   - Bulk deletion has no client-go equivalent, so BatchDelete issues one
//     DELETE /v1/project/{uuid} per project.
//   - A project's "active" flag is toggled via a partial update,
//     PATCH /v1/project/{uuid}. There is no bulk endpoint for this, so
//     deactivating multiple projects means one PATCH per project.
//   - PUT /v1/bom processes the upload asynchronously and returns a tracking
//     token immediately; GET /v1/event/token/{uuid} reports whether that
//     token's job is still queued/running. Clone and BOM-upload tokens are
//     polled through this same generic event-token endpoint.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	dtrack "github.com/DependencyTrack/client-go"
	"github.com/google/uuid"
)

// Project is a subset of the Dependency-Track "Project" schema.
type Project struct {
	UUID            string `json:"uuid"`
	Name            string `json:"name"`
	Version         string `json:"version,omitempty"`
	Classifier      string `json:"classifier,omitempty"`
	CollectionLogic string `json:"collectionLogic,omitempty"`
	Active          bool   `json:"active"`
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

// fromDTProject adapts a client-go Project into our own simplified shape.
func fromDTProject(p dtrack.Project) Project {
	logic := ""
	if p.CollectionLogic != nil {
		logic = string(*p.CollectionLogic)
	}
	return Project{
		UUID:            p.UUID.String(),
		Name:            p.Name,
		Version:         p.Version,
		Classifier:      p.Classifier,
		CollectionLogic: logic,
		Active:          p.Active,
	}
}

// parseUUID validates a caller-supplied UUID string, wrapping the error with
// context about which value failed since client-go itself only reports the
// parse failure in isolation.
func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid project uuid %q: %w", s, err)
	}
	return id, nil
}

// IsNotFound reports whether err represents an HTTP 404 response from the
// server, e.g. from GetProject/LookupProject on an unknown project. It lets
// callers branch on "not found" without depending on client-go's error type
// directly.
func IsNotFound(err error) bool {
	var apiErr *dtrack.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// Client is a thin wrapper around a *dtrack.Client, adapting it to the
// Project shape and method set this CLI needs.
type Client struct {
	dt *dtrack.Client
}

// Option customises the underlying client-go client.
type Option = dtrack.ClientOption

// WithHTTPClient overrides the underlying *http.Client (e.g. to change TLS
// verification or timeouts).
func WithHTTPClient(h *http.Client) Option {
	return dtrack.WithHttpClient(h)
}

// New builds a Client. baseURL may be given with or without a trailing
// "/api"; client-go's own paths already include "api/...", so any such
// suffix is stripped before handing the URL to it.
//
// Unlike a bare HTTP client, client-go's constructor immediately performs an
// (unauthenticated) GET /api/version call to validate connectivity and learn
// the server version, so New can fail on a network or server error, not just
// a malformed URL.
func New(baseURL, apiKey string, opts ...Option) (*Client, error) {
	root := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/api")

	// apiKey must be applied last: dtrack.WithAPIKey layers an auth-header
	// RoundTripper on top of whatever *http.Client is already set, but
	// dtrack.WithHttpClient *replaces* the client wholesale. Applying
	// WithAPIKey before a caller's WithHTTPClient would silently discard the
	// auth wrapping and send every request unauthenticated.
	all := append(append([]dtrack.ClientOption{}, opts...), dtrack.WithAPIKey(apiKey))

	dt, err := dtrack.NewClient(root, all...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Dependency-Track at %s: %w", root, err)
	}
	return &Client{dt: dt}, nil
}

// pageSize is the page size used for every paginated list call.
const pageSize = 100

// paginateProjects walks every page of a paginated project-list endpoint by
// repeatedly calling fetch with increasing page numbers, until a short page
// or the server-reported total count indicates there is nothing left.
func paginateProjects(fetch func(dtrack.PageOptions) (dtrack.Page[dtrack.Project], error)) ([]dtrack.Project, error) {
	var out []dtrack.Project
	page := 1
	for {
		res, err := fetch(dtrack.PageOptions{PageNumber: page, PageSize: pageSize})
		if err != nil {
			return nil, err
		}
		if len(res.Items) == 0 {
			return out, nil
		}
		out = append(out, res.Items...)
		if len(out) >= res.TotalCount || len(res.Items) < pageSize {
			return out, nil
		}
		page++
	}
}

// convertProjects adapts a slice of client-go Projects, optionally dropping
// inactive ones.
func convertProjects(items []dtrack.Project, excludeInactive bool) []Project {
	out := make([]Project, 0, len(items))
	for _, p := range items {
		proj := fromDTProject(p)
		if excludeInactive && !proj.Active {
			continue
		}
		out = append(out, proj)
	}
	return out
}

// ListProjectsByName returns every version of the project with the exact
// given name, optionally excluding inactive ones. Dependency-Track's "name"
// filter on GET /v1/project is an exact match, not a substring search.
func (c *Client) ListProjectsByName(ctx context.Context, name string, excludeInactive bool) ([]Project, error) {
	items, err := c.dt.Project.GetProjectsForName(ctx, name, excludeInactive, false)
	if err != nil {
		return nil, err
	}
	out := make([]Project, len(items))
	for i, p := range items {
		out[i] = fromDTProject(p)
	}
	return out, nil
}

// GetProject fetches a single project by UUID via GET /v1/project/{uuid}.
func (c *Client) GetProject(ctx context.Context, uuidStr string) (Project, error) {
	id, err := parseUUID(uuidStr)
	if err != nil {
		return Project{}, err
	}
	p, err := c.dt.Project.Get(ctx, id)
	if err != nil {
		return Project{}, err
	}
	return fromDTProject(p), nil
}

// LookupProject resolves a project by its exact name and version via
// GET /v1/project/lookup. Unlike ListProjectsByName, this returns a single
// project (or a "not found" error), since name+version is unique.
func (c *Client) LookupProject(ctx context.Context, name, version string) (Project, error) {
	p, err := c.dt.Project.Lookup(ctx, name, version)
	if err != nil {
		return Project{}, err
	}
	return fromDTProject(p), nil
}

// ListCollectionProjects returns all projects that act as collection parents.
func (c *Client) ListCollectionProjects(ctx context.Context, excludeInactive bool) ([]Project, error) {
	items, err := paginateProjects(func(po dtrack.PageOptions) (dtrack.Page[dtrack.Project], error) {
		return c.dt.Project.GetAll(ctx, po)
	})
	if err != nil {
		return nil, err
	}
	all := convertProjects(items, excludeInactive)
	out := make([]Project, 0, len(all))
	for _, p := range all {
		if p.IsCollection() {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListChildren returns the direct children of a project. In v5 this is a
// dedicated endpoint; the inline "children" array was removed from the
// project payload. client-go's GetChildren has no server-side
// excludeInactive filter, so it is applied client-side here instead.
func (c *Client) ListChildren(ctx context.Context, uuidStr string, excludeInactive bool) ([]Project, error) {
	id, err := parseUUID(uuidStr)
	if err != nil {
		return nil, err
	}
	items, err := paginateProjects(func(po dtrack.PageOptions) (dtrack.Page[dtrack.Project], error) {
		return c.dt.Project.GetChildren(ctx, id, po)
	})
	if err != nil {
		return nil, err
	}
	return convertProjects(items, excludeInactive), nil
}

// DeleteProject deletes a single project by UUID.
func (c *Client) DeleteProject(ctx context.Context, uuidStr string) error {
	id, err := parseUUID(uuidStr)
	if err != nil {
		return err
	}
	return c.dt.Project.Delete(ctx, id)
}

// BatchDelete deletes multiple projects. Dependency-Track's batch-delete
// endpoint has no client-go wrapper, so each project is deleted individually
// (the same fallback this client always used when the batch endpoint was
// unavailable).
func (c *Client) BatchDelete(ctx context.Context, uuids []string) error {
	for _, u := range uuids {
		if err := c.DeleteProject(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// DeactivateProject sets a single project's "active" flag to false via a
// partial update (PATCH /v1/project/{uuid}). Dependency-Track's patch
// handler reads only a fixed set of fields off the request body (active
// among them; metrics and lastBomImport are not among them), so sending a
// bare Project{Active: false} is safe and equivalent to a hand-written
// {"active": false} payload.
//
// If the project is already inactive, Dependency-Track's PATCH returns
// HTTP 304 Not Modified (nothing to change) rather than 200 — that is
// treated as success here, since "already deactivated" isn't a failure for
// a deactivate operation.
func (c *Client) DeactivateProject(ctx context.Context, uuidStr string) error {
	id, err := parseUUID(uuidStr)
	if err != nil {
		return err
	}
	_, err = c.dt.Project.Patch(ctx, id, dtrack.Project{Active: false})
	if err != nil {
		var apiErr *dtrack.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotModified {
			return nil
		}
		return err
	}
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

// CloneProject clones sourceUUID into a new project at newVersion, copying
// the related data selected by opts. Dependency-Track processes cloning
// asynchronously (it dispatches a background event and returns immediately):
// the returned token identifies that job, not the finished project.
func (c *Client) CloneProject(ctx context.Context, sourceUUID, newVersion string, opts CloneOptions) (string, error) {
	id, err := parseUUID(sourceUUID)
	if err != nil {
		return "", err
	}

	includePolicyViolations := opts.IncludePolicyViolations
	makeCloneLatest := opts.MakeCloneLatest
	req := dtrack.ProjectCloneRequest{
		ProjectUUID: id,
		Version:     newVersion,
		IncludeTags: opts.IncludeTags,
		// client-go's ProjectCloneRequest has no separate "includeDependencies"
		// field. Server-side, that flag only ever does one thing: force
		// includeComponents to true ("for backward compatibility" — verified
		// against Dependency-Track's CloneProjectRequest constructor). ORing
		// it into IncludeComponents here reproduces that exactly.
		IncludeComponents:       opts.IncludeComponents || opts.IncludeDependencies,
		IncludeProperties:       opts.IncludeProperties,
		IncludeServices:         opts.IncludeServices,
		IncludeAuditHistory:     opts.IncludeAuditHistory,
		IncludeACL:              opts.IncludeACL,
		IncludePolicyViolations: &includePolicyViolations,
		MakeCloneLatest:         &makeCloneLatest,
	}

	token, err := c.dt.Project.Clone(ctx, req)
	if err != nil {
		return "", err
	}
	return string(token), nil
}

// BOMUploadOptions identifies the project to upload a BOM to and any
// versioning behavior for the upload (PUT /v1/bom).
type BOMUploadOptions struct {
	// ProjectUUID identifies an existing project directly. When set, the
	// Name/Version/AutoCreate/Parent* fields below are ignored by the server.
	ProjectUUID string

	// Name and Version identify the project by name: Dependency-Track
	// resolves them to an existing project, or creates one when AutoCreate
	// is set (optionally under the project named by Parent*).
	Name    string
	Version string

	AutoCreate    bool
	ParentName    string
	ParentVersion string
	ParentUUID    string

	// IsLatest marks the uploaded BOM as belonging to the latest version of
	// the project.
	IsLatest bool
}

// UploadBOM uploads a base64-encoded CycloneDX BOM via PUT /v1/bom. The
// target project is identified either by opts.ProjectUUID or by
// opts.Name/opts.Version (optionally auto-created). The upload is processed
// asynchronously; the returned token can be polled with IsTokenProcessing.
func (c *Client) UploadBOM(ctx context.Context, bomBase64 string, opts BOMUploadOptions) (string, error) {
	req := dtrack.BOMUploadRequest{
		ProjectName:    opts.Name,
		ProjectVersion: opts.Version,
		ParentName:     opts.ParentName,
		ParentVersion:  opts.ParentVersion,
		AutoCreate:     opts.AutoCreate,
		IsLatest:       &opts.IsLatest,
		BOM:            bomBase64,
	}

	if opts.ProjectUUID != "" {
		id, err := parseUUID(opts.ProjectUUID)
		if err != nil {
			return "", err
		}
		req.ProjectUUID = &id
	}
	if opts.ParentUUID != "" {
		id, err := parseUUID(opts.ParentUUID)
		if err != nil {
			return "", err
		}
		req.ParentUUID = &id
	}

	token, err := c.dt.BOM.Upload(ctx, req)
	if err != nil {
		return "", err
	}
	return string(token), nil
}

// IsTokenProcessing reports whether the background job identified by token
// is still queued or running, via GET /v1/event/token/{uuid}. Dependency-
// Track uses this same generic tracking token for every async job dispatched
// through an event (BOM uploads, project clones, ...).
func (c *Client) IsTokenProcessing(ctx context.Context, token string) (bool, error) {
	return c.dt.Event.IsBeingProcessed(ctx, dtrack.EventToken(token))
}
