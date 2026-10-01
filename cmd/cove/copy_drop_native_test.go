package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
)

func TestCopyDropPathsNativePasteboard(t *testing.T) {
	objc.AutoreleasePool(func() {
		pb := appkit.GetNSPasteboardClass().PasteboardWithUniqueName()
		defer pb.ReleaseGlobally()
		urls := []objectivec.IObject{
			foundation.NewURLFileURLWithPath("/Users/tmc/My Files/日本語 'report'.txt"),
			foundation.NewURLWithString("https://example.com/file.txt"),
			foundation.NewURLFileURLWithPath("/Volumes/Data/folder"),
			foundation.NewURLFileURLWithPath("/"),
		}
		if !pb.WriteObjects(urls) {
			t.Fatal("could not write unique test pasteboard")
		}
		class, err := objc.RegisterClass(fmt.Sprintf("CoveCopyDropSenderTest_%d", copyDropClassCount.Add(1)), objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
			{Cmd: objc.RegisterName("draggingPasteboard"), Fn: func(_ objc.ID, _ objc.SEL) objc.ID { return pb.ID }},
		})
		if err != nil {
			t.Fatal(err)
		}
		sender := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(class), objc.Sel("alloc")), objc.Sel("init"))
		defer objc.Send[objc.ID](sender, objc.Sel("release"))
		got := copyDropPaths(sender)
		want := []string{"/Users/tmc/My Files/日本語 'report'.txt", "/Volumes/Data/folder"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paths = %#v, want %#v", got, want)
		}
		pb.ClearContents()
		if got := copyDropPaths(sender); len(got) != 0 {
			t.Fatalf("empty pasteboard = %#v", got)
		}
	})
}
