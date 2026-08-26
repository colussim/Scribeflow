mermaid.min.js est téléchargé lors du build (voir Makefile / build.sh) et
embarqué dans le binaire via go:embed. Ce fichier n'est pas versionné tel
quel dans le dépôt : lancez `./scripts/fetch-mermaid.sh` avant `go build`
si `mermaid.min.js` est absent de ce dossier.
