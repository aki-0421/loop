package assets

import "embed"

//go:embed templates/loop.config.yaml templates/skills/*/SKILL.md schemas/*.json
var FS embed.FS

func Read(name string) ([]byte, error) {
	return FS.ReadFile(name)
}

func ReadTemplate(name string) ([]byte, error) {
	return FS.ReadFile("templates/" + name)
}

func ReadSchema(name string) ([]byte, error) {
	return FS.ReadFile("schemas/" + name)
}
