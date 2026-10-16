// Package resources is the contract every kind shown by k7s implements; the
// kinds themselves are its sub folders (pods, secrets, gitops, …).
//
// A kind watches its objects in the current namespace (and filter) and says
// what a row shows (row.go: columns and cells with a tone — no styling).
// What a row can do are Commands (command.go), in the kind's commands
// folder; each leads to a Picker, a Viewer or an Exec (steps.go).
//
// The tui draws all of it the same way for every kind. Adding a kind means
// adding a folder and listing it (with its commands) in the tui registry.
package resources

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fu/k7s/internal/kube"
)

// Resource is one kind on the page: a section with a table and the watch
// that keeps it current.
type Resource interface {
	// Name is the section title and menu entry; its first letter toggles it
	// (or the key a Keyed kind names).
	Name() string
	// Tag is its code in the change log, at most three letters: kubectl's
	// short name where it has one (po, rs, pvc), else our own (dp, se).
	Tag() string
	// Objects are the API kinds it shows (e.g. "Pod"), so Kubernetes events
	// about them get its tag.
	Objects() []string
	// Start (re)starts watching the host's namespace and filter; Stop ends it.
	Start(h Host) tea.Cmd
	Stop()
	// Update handles the resource's own messages (and ignores the others).
	Update(msg tea.Msg) tea.Cmd
	// Available reports whether the section exists on this cluster at all.
	Available() bool
	// Ready reports whether the first list has arrived.
	Ready() bool

	// Columns and Rows are the section's table, rows in display order.
	Columns() []Column
	Rows() []Row
	// Empty is shown instead of a table without rows.
	Empty() string
	// Status is its part of the status line (none if empty).
	Status() []Cell

	// Listed returns the objects the section shows (and their owners, such
	// as a pod's Deployment), so other kinds can relate to them.
	Listed() []Ref
}

// Labeled is a kind with its own API resource; the filter applies to it and
// suggests label selectors from it.
type Labeled interface {
	GVR() schema.GroupVersionResource
}

// Keyed is a kind whose toggle key is not the first letter of its name
// (that one is taken); the key must be a letter of the name, e.g. "m" for
// clai[m]s.
type Keyed interface {
	Key() string
}

// Stateful is a kind whose rows are in named states (Row.State), e.g. pods:
// running, pending, failing, completed. The user can hide rows by state.
type Stateful interface {
	States() []string
}

// Linker is a kind whose objects lead to others: an app deploys a
// Deployment, a Deployment runs a pod, a pod uses a secret. Selecting a row
// highlights every row linked to it, both ways and across kinds.
type Linker interface {
	Links() []Link
}

// Link says From leads to To. Either end may be an object no section shows
// (such as a Deployment); links still pass through it.
type Link struct {
	From, To Ref
}

// Host is what a kind gets from the app.
type Host interface {
	Context() context.Context // ends when k7s quits; Start derives its own per scope
	Client() *kube.Client
	Namespace() string
	Selector() string
	Refresh() time.Duration
	Send(tea.Msg)       // deliver a message from a background goroutine
	Log(Change)         // add to the change log
	Notice(string)      // a short message in the navigation bar
	Listed() []Ref      // the objects all sections list
	Pulse(at time.Time) // fresh data arrived: the "watching" dot peaks
}

// Ref names one Kubernetes object.
type Ref struct {
	Kind, Namespace, Name string
}

// Change is one change-log entry; the tui formats and wraps it.
type Change struct {
	At     time.Time
	Tag    string // which kind it is about (filled in from the kind that logs it)
	Kind   ChangeKind
	Object string   // e.g. "web-7d4-x", "secret/tls"
	State  []Cell   // e.g. the pod status with its light
	Detail string   // what changed and why
	Items  []string // one extra line each, e.g. every degraded resource
	// Show, if set, decides when drawing whether the entry belongs on the
	// page (e.g. changes of apps not responsible for what is listed).
	Show func() bool
}

type ChangeKind string

const (
	Added    ChangeKind = "ADDED"
	Modified ChangeKind = "MODIFIED"
	Deleted  ChangeKind = "DELETED"
	Warning  ChangeKind = "WARNING"
	Event    ChangeKind = "EVENT"
	State    ChangeKind = "STATE" // already unhealthy when k7s started
	Forward  ChangeKind = "FORWARD"
)
