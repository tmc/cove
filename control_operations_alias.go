package main

import "github.com/tmc/vz-macos/internal/control/operations"

type Operation = operations.Operation
type OperationProgress = operations.OperationProgress
type OperationError = operations.OperationError
type OperationStore = operations.OperationStore
type OperationRegistry = operations.OperationRegistry
type FileOperationStore = operations.FileOperationStore
type MemOperationStore = operations.MemOperationStore

var ErrOperationNotFound = operations.ErrOperationNotFound

var NewOperationRegistry = operations.NewOperationRegistry
var NewFileOperationStore = operations.NewFileOperationStore
var NewMemOperationStore = operations.NewMemOperationStore
