package main

import (
	"sync/atomic"

	"github.com/tmc/apple/foundation"
)

type uiTask struct {
	fn   func()
	done chan uiTaskResult
}

type uiTaskResult struct {
	panicValue any
}

var (
	uiThreadID    atomic.Uintptr
	uiThreadTasks = make(chan uiTask, 256)
)

func currentUIThreadID() uintptr {
	return uintptr(foundation.GetThreadClass().CurrentThread().ID)
}

func registerUIThread() {
	uiThreadID.Store(currentUIThreadID())
}

func onUIThread() bool {
	id := uiThreadID.Load()
	return id != 0 && currentUIThreadID() == id
}

func runOnUIThreadSync(fn func()) {
	if fn == nil {
		return
	}
	if onUIThread() || uiThreadID.Load() == 0 {
		fn()
		return
	}
	done := make(chan uiTaskResult, 1)
	uiThreadTasks <- uiTask{fn: fn, done: done}
	result := <-done
	if result.panicValue != nil {
		panic(result.panicValue)
	}
}

func runOnUIThreadAsync(fn func()) {
	if fn == nil {
		return
	}
	if onUIThread() || uiThreadID.Load() == 0 {
		fn()
		return
	}
	uiThreadTasks <- uiTask{fn: fn}
}

func drainUIThreadTasks() {
	if !onUIThread() {
		return
	}
	for {
		select {
		case task := <-uiThreadTasks:
			executeUITask(task)
		default:
			return
		}
	}
}

func executeUITask(task uiTask) {
	if task.done == nil {
		task.fn()
		return
	}
	var result uiTaskResult
	defer func() {
		if r := recover(); r != nil {
			result.panicValue = r
		}
		task.done <- result
	}()
	task.fn()
}
