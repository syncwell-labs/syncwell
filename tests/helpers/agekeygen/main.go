// Command agekeygen creates an ephemeral age identity for the encrypted
// restore-drill integration test. It is not shipped in the server binary.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"filippo.io/age"
)

func main() {
	out := flag.String("o", "", "private identity output file")
	flag.Parse()
	if *out == "" {
		log.Fatal("-o identity file is required")
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, []byte(identity.String()+"\n"), 0o600); err != nil {
		log.Fatal(err)
	}
	if err := os.Chmod(*out, 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Public key: %s\n", identity.Recipient())
}
