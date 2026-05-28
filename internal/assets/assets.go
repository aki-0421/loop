package assets

import "embed"

//go:embed templates/loop.config.yaml
var FS embed.FS

func ReadTemplate(name string) ([]byte, error) {
	return FS.ReadFile("templates/" + name)
}
