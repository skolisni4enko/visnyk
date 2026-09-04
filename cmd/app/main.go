package main

import (
	"fmt"
	"log"
)

// main is the entrypoint — only wiring, no business logic (see AGENTS.md).
func main() {
	fmt.Println("visnyk — каскадна розсилка WA → TG → Viber")
	fmt.Println("MVP skeleton. Implement internal/cascade, whatsapp, telegram.")
	log.Println("run with: go run ./cmd/app")
}
