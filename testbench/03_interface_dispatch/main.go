// TEST: Interface dispatch hides call edges.
// EXPECT: callers of concrete methods show 0 when called through interfaces.
// The `dead` command already warns about this, but let's make it painful.
package main

import "fmt"

// --- Layer 1: Simple interface dispatch ---

type Writer interface {
	Write(data []byte) error
}

type FileWriter struct{}

func (f *FileWriter) Write(data []byte) error {
	fmt.Println("file:", string(data))
	return nil
}

type NetWriter struct{}

func (n *NetWriter) Write(data []byte) error {
	fmt.Println("net:", string(data))
	return nil
}

func writeAll(w Writer, data []byte) error {
	return w.Write(data) // Who is called? FileWriter.Write? NetWriter.Write?
}

// --- Layer 2: Interface embedding / composition ---

type ReadWriter interface {
	Reader
	Writer
}

type Reader interface {
	Read(buf []byte) (int, error)
}

type Socket struct{}

func (s *Socket) Read(buf []byte) (int, error) { return 0, nil }
func (s *Socket) Write(data []byte) error      { return nil }

// Passed as ReadWriter, but individual methods dispatched separately.
func copyData(rw ReadWriter) {
	buf := make([]byte, 1024)
	rw.Read(buf)
	rw.Write(buf)
}

// --- Layer 3: Empty interface / any ---

func blackHole(v any) {
	if w, ok := v.(Writer); ok {
		w.Write([]byte("surprise")) // Type assertion reveals interface, then dispatch.
	}
}

// --- Layer 4: Interface satisfaction across packages (simulated) ---

type Processor interface {
	Process() error
}

// Satisfies Processor but NEVER explicitly assigned to Processor in code.
// Only passed via `any` → type assertion chain.
type SneakyProcessor struct{}

func (sp *SneakyProcessor) Process() error {
	fmt.Println("sneaky processing")
	return nil
}

func maybeProcess(v any) {
	if p, ok := v.(Processor); ok {
		p.Process() // SneakyProcessor.Process called, but no static evidence.
	}
}

// --- Layer 5: Interface with same method names, different interfaces ---

type Stringer interface {
	String() string
}

type Debugger interface {
	String() string // Same method name, different interface!
}

type Hybrid struct{}

func (h *Hybrid) String() string { return "hybrid" }

func printStringer(s Stringer) { fmt.Println(s.String()) }
func debugIt(d Debugger)       { fmt.Println(d.String()) }

func main() {
	writeAll(&FileWriter{}, []byte("hello"))
	writeAll(&NetWriter{}, []byte("world"))

	copyData(&Socket{})

	blackHole(&FileWriter{})
	maybeProcess(&SneakyProcessor{})

	h := &Hybrid{}
	printStringer(h)
	debugIt(h)
}
