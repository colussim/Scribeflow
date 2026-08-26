---
title: Exemple de page
space: DEV
parent: Documentation technique
labels: [demo, architecture]
---

# Exemple de page

Ceci est un paragraphe avec du **gras**, de l'*italique*, du texte ~~barré~~
et du `code inline`. Voici un [lien](https://example.com).

## Diagramme

```mermaid
flowchart TD
    A[Client] -->|requête| B(API Gateway)
    B --> C{Cache?}
    C -->|hit| D[Retour immédiat]
    C -->|miss| E[Service backend]
    E --> F[(Base de données)]
    E --> D
```

## Liste de tâches

- [x] Écrire le convertisseur markdown
- [x] Gérer les diagrammes mermaid
- [ ] Tester sur Windows
- [ ] Tester sur macOS

## Tableau

| Composant | Langage | Statut |
|---|:-:|--:|
| Convertisseur | Go | OK |
| Rendu Mermaid | Go + Chrome | OK |
| Client API | Go | OK |

## Image locale

![Logo](./logo.png)

## Code

```go
func main() {
    fmt.Println("hello")
}
```

> Une citation pour vérifier le rendu blockquote.

---

Fin de l'exemple.
