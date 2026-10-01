package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
)

func TestCopyProgressContext(t *testing.T) {
	var got [][2]int64
	ctx := WithCopyProgress(context.Background(), func(bytes, total int64) { got = append(got, [2]int64{bytes, total}) })
	reportCopyProgress(context.Background(), 1, 2)
	reportCopyProgress(ctx, 3, 4)
	if len(got) != 1 || got[0] != [2]int64{3, 4} {
		t.Fatalf("progress = %v", got)
	}
}

func ExampleWithCopyProgress() {
	ctx := WithCopyProgress(context.Background(), func(bytes, total int64) { fmt.Printf("%d of %d bytes\n", bytes, total) })
	reportCopyProgress(ctx, 32, 64)
	// Output: 32 of 64 bytes
}

func ExampleCopyProgressWriter() {
	ctx := WithCopyProgress(context.Background(), func(bytes, total int64) { fmt.Printf("%d bytes\n", bytes) })
	w := CopyProgressWriter(ctx, io.Discard)
	w.Write([]byte("hello"))
	// Output: 5 bytes
}

func TestCopyProgressWriter(t *testing.T) {
	var got [][2]int64
	ctx := WithCopyProgress(context.Background(), func(bytes, total int64) { got = append(got, [2]int64{bytes, total}) })
	var data bytes.Buffer
	w := CopyProgressWriter(ctx, &data)
	w.Write([]byte("one"))
	w.Write([]byte("two"))
	if data.String() != "onetwo" || !reflect.DeepEqual(got, [][2]int64{{3, 0}, {6, 0}}) {
		t.Fatalf("output %q, progress %v", data.String(), got)
	}
}
