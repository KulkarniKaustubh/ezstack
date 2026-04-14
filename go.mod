// Deprecated: This module has moved to github.com/KulkarniKaustubh/ezstack/v4.
// Please update your import paths to use the /v4 suffix. Any tag on this
// (non-/v4) module path is retracted; all future development lives at
// github.com/KulkarniKaustubh/ezstack/v4.
module github.com/KulkarniKaustubh/ezstack

go 1.25.1

// Retract every pre-/v4 tag so `go get` / `go install` on this path
// resolves to a deprecation warning instead of stale code, and so
// proxy.golang.org stops handing it out as "latest" for the old path.
retract (
	[v0.0.0, v1.11.5]
)

require (
	github.com/chzyer/readline v1.5.1
	github.com/mattn/go-runewidth v0.0.19
	github.com/spf13/pflag v1.0.10
	github.com/spf13/viper v1.21.0
	golang.org/x/term v0.39.0
)

require (
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.5.0 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.28.0 // indirect
)
