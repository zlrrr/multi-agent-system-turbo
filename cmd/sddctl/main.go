// Command sddctl enforces the specification-driven development discipline this
// project is built on: bilingual parity, cascade freshness, and requirement
// coverage (Constitution Articles I-III).
//
// It exists because a process that is only written down is a process that drifts.
package main

import (
	"fmt"
	"os"

	"github.com/zlrrr/multi-agent-system-turbo/internal/sdd"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "verify":
		report, err := sdd.Verify(sdd.RepoRoot())
		if err != nil {
			fmt.Fprintf(os.Stderr, "sddctl: %v\n", err)
			os.Exit(2)
		}
		report.Print(os.Stdout)
		if report.Failed() {
			os.Exit(1)
		}
	case "amend":
		if err := sdd.Amend(sdd.RepoRoot(), os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "sddctl: %v\n", err)
			os.Exit(2)
		}
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`sddctl — specification-driven development checks

Usage:
  sddctl verify    Check bilingual parity, cascade freshness and requirement coverage
  sddctl amend --feature <id> --artifact <name> --version <x.y.z>
                   Stamp an artifact's version and mark its downstream as reviewed

Checks performed by verify:
  parity        every document under docs/, specs/ and .specify/ has both an
                English and a Simplified Chinese counterpart
  staleness     no artifact was derived from an older upstream version than the
                one currently in the repository (Article II.2)
  coverage      every FR-### in a specification is claimed by at least one task,
                and every task cites requirements that exist (Article I.3)
  changelog     every versioned artifact records its version in a Change Log`)
}
