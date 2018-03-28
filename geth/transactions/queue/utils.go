package queue

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime/debug"

	"github.com/status-im/status-go/geth/signal"
)

//ErrTxQueueRunFailure - error running transaction queue
var ErrTxQueueRunFailure = errors.New("error running transaction queue")

// HaltOnPanic recovers from panic, logs issue, sends upward notification, and exits
func HaltOnPanic() {
	if r := recover(); r != nil {
		err := fmt.Errorf("%v: %v", ErrTxQueueRunFailure, r)

		// send signal up to native app
		signal.Send(signal.Envelope{
			Type: signal.EventNodeCrashed,
			Event: signal.NodeCrashEvent{
				Error: err,
			},
		})

		Fatalf(err) // os.exit(1) is called internally
	}
}

// Fatalf is used to halt the execution.
// When called the function prints stack end exits.
// Failure is logged into both StdErr and StdOut.
func Fatalf(reason interface{}, args ...interface{}) {
	// decide on output stream
	w := io.MultiWriter(os.Stdout, os.Stderr)
	outf, _ := os.Stdout.Stat() // nolint: gas
	errf, _ := os.Stderr.Stat() // nolint: gas
	if outf != nil && errf != nil && os.SameFile(outf, errf) {
		w = os.Stderr
	}

	// find out whether error or string has been passed as a reason
	r := reflect.ValueOf(reason)
	if r.Kind() == reflect.String {
		fmt.Fprintf(w, "Fatal Failure: %v\n%v\n", reason.(string), args)
	} else {
		fmt.Fprintf(w, "Fatal Failure: %v\n", reason.(error))
	}

	debug.PrintStack()

	os.Exit(1)
}
