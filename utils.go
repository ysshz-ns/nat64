package main

import (
	"log"
	"reflect"
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

func GetChannelValue[T any](ch chan T) (value T, resulted bool, closed bool) {
	select {
	case value, closed := <-ch:
		return value, true, closed
	default:
		resulted = false
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
