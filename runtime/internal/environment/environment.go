package environment

import "context"

// Capability describes what an environment can technically do. Authority is
// kept separately because an available tool is not automatically permission to
// use it.
type Capability string

const (
	CanShell      Capability = "shell"
	CanReadFiles  Capability = "read-files"
	CanWriteFiles Capability = "write-files"
	CanGit        Capability = "git"
	CanSSH        Capability = "ssh"
	CanBuild      Capability = "build"
	CanUseDocker  Capability = "docker"
)

type Paths struct {
	Home      string `json:"home"`
	Workspace string `json:"workspace"`
	Config    string `json:"config"`
	Cache     string `json:"cache"`
}

type Tool struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Available bool   `json:"available"`
}

type Authority struct {
	Mode    string       `json:"mode"`
	Granted []Capability `json:"granted"`
}

type Info struct {
	Kind         string       `json:"kind"`
	Profile      string       `json:"profile"`
	OS           string       `json:"os"`
	Arch         string       `json:"arch"`
	Paths        Paths        `json:"paths"`
	Capabilities []Capability `json:"capabilities"`
	Authority    Authority    `json:"authority"`
	Tools        []Tool       `json:"tools"`
}

type RunSpec struct {
	Command        string
	Args           []string
	Stdin          string
	WorkDir        string
	Isolated       bool
	Requires       []Capability
	MaxOutputBytes int
}

type RunResult struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	StdoutTruncated bool
	StderrTruncated bool
}

// Environment is the replaceable execution boundary used by the core. An
// implementation may be local today and LXC, Docker, or Kubernetes later.
type Environment interface {
	Prepare(context.Context) error
	Inspect(context.Context) (Info, error)
	LookPath(context.Context, string) (string, error)
	ReadFile(context.Context, string) ([]byte, error)
	Run(context.Context, RunSpec) (RunResult, error)
}
