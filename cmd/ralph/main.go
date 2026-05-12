// Command ralph is an FSM-driven autonomous-loop CLI ("ralph fsm").
//
// See docs/concepts/ralph-fsm.md for the methodology and
// docs/concepts/state-machine.md for the topology.
package main

import (
	"os"

	"github.com/jcrussell/ralph/internal/ralphcmd"
)

func main() {
	os.Exit(ralphcmd.Run(os.Args[1:]))
}
