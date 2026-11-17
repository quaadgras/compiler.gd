// js_page.go is the GOOS=js program of js_page_test.mjs: it runs in a
// page's main thread, with the page's document.
package main

import (
	"fmt"
	"os"
	"syscall/js"
)

func main() {
	doc := js.Global().Get("document")
	p := doc.Call("createElement", "p")
	p.Set("id", "greeting")
	p.Set("textContent", "hello from Go")
	doc.Get("body").Call("appendChild", p)
	fmt.Println("greeting:", doc.Call("getElementById", "greeting").Get("textContent").String())

	// A listener that JavaScript calls while Go waits in click.
	clicks := 0
	button := doc.Call("createElement", "button")
	onClick := js.FuncOf(func(this js.Value, args []js.Value) any {
		clicks++
		this.Set("textContent", fmt.Sprintf("clicked %d", clicks))
		return nil
	})
	button.Call("addEventListener", "click", onClick)
	doc.Get("body").Call("appendChild", button)
	for range 3 {
		button.Call("click")
	}
	fmt.Println("button:", button.Get("textContent").String(), "event type:", js.Global().Get("Event").Get("name").String())

	// A Go function that JavaScript calls and gets a result from.
	add := js.FuncOf(func(this js.Value, args []js.Value) any {
		return args[0].Int() + args[1].Int()
	})
	js.Global().Set("goAdd", add)
	fmt.Println("goAdd(2, 3) from JavaScript:", js.Global().Call("eval", "goAdd(2, 3)").Int())

	// Bytes both ways.
	u8 := js.Global().Get("Uint8Array").New(4)
	js.CopyBytesToJS(u8, []byte{1, 2, 3, 4})
	back := make([]byte, 4)
	js.CopyBytesToGo(back, u8)
	fmt.Println("bytes:", back, u8.Call("join", "-").String())

	// Files: those of the page (memFS, from runGo's files).
	data, err := os.ReadFile("/data/in.txt")
	fmt.Printf("read: %q %v\n", data, err)
	err = os.WriteFile("/tmp/out.txt", []byte("written by Go"), 0o644)
	fmt.Println("write:", err)
	js.Global().Set("goWrote", "/tmp/out.txt")

	// A callback from the event loop, later: main waits for it.
	done := make(chan string)
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		done <- "timeout fired"
		return nil
	})
	js.Global().Call("setTimeout", cb, 10)
	fmt.Println(<-done)
	cb.Release()
	os.Exit(0)
}
