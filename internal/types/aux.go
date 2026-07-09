package types

// RegistryRef is an entry in the depot's registries.json (§5).
type RegistryRef struct {
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	GitURL string `json:"giturl"`
}

// WorkspaceEntry is a shared develop checkout in dev/workspace.json (§12.7).
type WorkspaceEntry struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	Major   int    `json:"major"`
	GitURL  string `json:"giturl"`
	Ref     string `json:"ref"`
	RefKind string `json:"refKind"` // "branch" | "tag"
	Path    string `json:"path"`    // depot-relative
}

// Workspace is dev/workspace.json — the co-development checkout set (§12.7).
type Workspace struct {
	SchemaVersion int              `json:"schemaVersion"`
	Entries       []WorkspaceEntry `json:"entries"`
}

// Enrollment is a project's git-ignored .cosm/develop.json (§7.4): the unit
// keys ("<uuid>@v<major>") this project opts into from the dev workspace.
type Enrollment struct {
	SchemaVersion int      `json:"schemaVersion"`
	Enrolled      []string `json:"enrolled"`
}
