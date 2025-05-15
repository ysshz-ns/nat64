package main

import (
	"bufio"
	"log"
	"reflect"
	"sync"
	"syscall"
)

func Must[T any](ret T, err error) T {
	if err != nil {
		log.Panic(err)
	}

	return ret
}

func Assert(err error) {
	if err != nil {
		log.Panic(err)
	}
}

// GetChannelValue attempts a non-blocking read on the provided channel `ch`.
func GetChannelValue[T any](ch chan T) (value T, blocking bool, closed bool) {
	select {
	case value, closed := <-ch:
		return value, true, closed
	default:
		blocking = false
		return
	}
}

func Control(sc syscall.RawConn, f func(fd uintptr) error) (err error) {
	if e := sc.Control(func(fd uintptr) {
		err = f(fd)
	}); e != nil {
		err = e
	}
	return
}

func Select[T any](chans []chan T) (index int, value T, ok bool) {
	rv := make([]reflect.SelectCase, len(chans))
	t := reflect.Value{}
	for i, v := range chans {
		rv[i] = reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(v),
			Send: t,
		}
	}

	index, t, ok = reflect.Select(rv)
	if ok && !t.IsNil() {
		value = t.Interface().(T)
	}

	return
}

// Task represents an asynchronous operation that may produce a value of type T.
// It provides methods to set the result, register callbacks, and wait for completion.
type Task[T any] struct {
	c          *sync.Cond
	value      T
	hasValue   bool
	onComplete []func(T)
}

// NewTask creates and returns a new, unresolved Task.
//
// Usage:
//
//	task := NewTask[int]()
func NewTask[T any]() *Task[T] {
	return &Task[T]{
		c: sync.NewCond(&sync.Mutex{}),
	}
}

// Resolve completes the Task with the given value.
// If the Task is already resolved, Resolve does nothing.
// It notifies all goroutines waiting on the Task and executes any registered 'Then' callbacks.
func (r *Task[T]) Resolve(value T) {
	if r.hasValue {
		return
	}
	r.c.L.Lock()
	if r.hasValue {
		r.c.L.Unlock()
		return
	}
	r.value = value
	r.hasValue = true
	onComplete := r.onComplete
	r.onComplete = nil
	r.c.Broadcast()
	r.c.L.Unlock()
	for _, f := range onComplete {
		f(value)
	}
}

// Then registers a callback function f to be executed when the Task is resolved.
// If the Task is already resolved, the callback is executed immediately in the calling goroutine.
// Callbacks are executed in the order they were added.
//
// Usage:
//
//	task.Then(func(result int) { fmt.Printf("Task completed with: %d\n", result) })
func (r *Task[T]) Then(f func(T)) {
	if r.hasValue {
		f(r.value)
		return
	}

	r.c.L.Lock()
	if r.hasValue {
		r.c.L.Unlock()
		f(r.value)
		return
	}
	r.onComplete = append(r.onComplete, f)
	r.c.L.Unlock()
}

// Done returns a read-only channel that receives the Task's result when it is resolved.
// If the Task is already resolved, a buffered channel with the result is returned.
// The channel will be closed after the value is sent.
//
// Usage: result := <-task.Done()
func (r *Task[T]) Done() <-chan T {
	ch := make(chan T, 1)
	if r.hasValue {
		ch <- r.value
		close(ch)
		return ch
	}

	go func() {
		r.c.L.Lock()
		for !r.hasValue {
			r.c.Wait()
		}
		r.c.L.Unlock()
		ch <- r.value
		close(ch)
	}()
	return ch
}

// TryGetResult attempts to retrieve the result of the Task without blocking.
// It returns the result and true if the Task is resolved.
// Otherwise, it returns the zero value for T and false.
//
// Usage: if val, ok := task.TryGetResult(); ok { /* use val */ }
func (r *Task[T]) TryGetResult() (value T, ok bool) {
	if r.hasValue {
		return r.value, true
	}

	r.c.L.Lock()
	if r.hasValue {
		r.c.L.Unlock()
		return r.value, true
	}
	r.c.L.Unlock()
	return
}

// GetResult waits for the Task to be resolved and then returns its result.
// If the Task is already resolved, it returns the result immediately.
// This method will block the calling goroutine until the Task is resolved.
func (r *Task[T]) GetResult() T {
	if r.hasValue {
		return r.value
	}

	r.c.L.Lock()
	for !r.hasValue {
		r.c.Wait()
	}
	r.c.L.Unlock()
	return r.value
}

// GetReadBytes attempts to read all currently buffered bytes from the provided bufio.Reader.
// It reads only what is immediately available in the buffer without blocking for more data.
// Returns the bytes read and any error encountered during the read operation.
func GetReadBytes(r *bufio.Reader) (ret []byte, err error) {
	l, buffered := 0, r.Buffered()
	ret = make([]byte, buffered)

	for {
		if l >= buffered {
			return ret[:l], nil
		}

		if n, err := r.Read(ret[l:]); err != nil {
			return ret, err
		} else {
			l += n
		}
	}
}
