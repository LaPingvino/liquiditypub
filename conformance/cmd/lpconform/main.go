// lpconform runs black-box conformance checks against live nodes:
//
//	lpconform https://riverside.example                     # one node, read surface
//	lpconform https://riverside.example https://hilltop.example  # + cross-check
package main

import (
	"fmt"
	"os"

	"github.com/LaPingvino/liquiditypub/conformance/runner"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: lpconform <node-base-url> [peer-base-url]")
		os.Exit(2)
	}
	failed := 0
	report := func(rs []runner.Result) {
		for _, r := range rs {
			mark := "ok  "
			if !r.OK {
				mark = "FAIL"
				failed++
			}
			fmt.Printf("  %s %s", mark, r.Check)
			if r.Note != "" {
				fmt.Printf("  (%s)", r.Note)
			}
			fmt.Println()
		}
	}

	fmt.Println(os.Args[1])
	rs, _, cpA := runner.CheckNode(os.Args[1])
	report(rs)

	if len(os.Args) == 3 {
		fmt.Println(os.Args[2])
		rs, _, cpB := runner.CheckNode(os.Args[2])
		report(rs)
		if cpA != nil && cpB != nil {
			fmt.Println("cross-check")
			report(runner.CrossCheck(cpA, cpB))
		}
	}
	if failed > 0 {
		fmt.Printf("\n%d check(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nall checks passed")
}
