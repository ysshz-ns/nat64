package main

import (
	"log"
	"os"
	"reflect"
	"strings"
)

var args Args

// Args is the struct containing the command line arguments.
// It supports 3 types of arguments:
// - Flags: arguments that do not take a value, e.g. --help. They are represented by a boolean field.
// - Options: arguments that take a value, e.g. --port 8080. They are represented by a string.
// - Multiple values: arguments that can be specified multiple times, e.g. --file file1 --file file2. They are represented by a slice of strings.
type Args struct {
	TcpPort string `arg:"-p,--tcp-port" help:"TCP Port to listen on. Default port is 8888. Set to 0 to disable."`
	UdpPort string `arg:"-P,--udp-port" help:"UDP Port to listen on. Default port is 8888. Set to 0 to disable."`
	Brutal  string `arg:"-w,--window" help:"TCP window size. Requires tcp-brutal kernel module."`
	Help    bool   `arg:"-h,--help" help:"Show this help message"`

	brutal int
}

type ArgListItem struct {
	Aliases  []string
	Help     string
	field    reflect.Value
	HasValue bool
	isSlice  bool
	isBool   bool
}

// ParseArgs is a minimal parser for command line arguments.
func ParseArgs[T any](args *T) (argList []ArgListItem, rest []string) {
	value := reflect.ValueOf(args).Elem()
	typ := value.Type()

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Tag == "" {
			continue
		}

		kind := field.Type.Kind()
		if kind != reflect.Bool && kind != reflect.String && kind != reflect.Slice {
			log.Panic("Unsupported arg type: " + field.Type.String())
		}

		argList = append(argList, ArgListItem{
			Aliases: strings.Split(field.Tag.Get("arg"), ","),
			Help:    field.Tag.Get("help"),
			field:   value.Field(i),
			isSlice: kind == reflect.Slice,
			isBool:  kind == reflect.Bool,
		})
	}

	commandline := os.Args[1:]
	for i := 0; i < len(commandline); {
		// not performance critical, so we can use a simple loop
		matched := false
		for _, arg := range argList {
			if arg.HasValue && !arg.isSlice {
				continue
			}

			for _, alias := range arg.Aliases {
				if commandline[i] == alias {
					if arg.isBool {
						arg.field.SetBool(true)
						arg.HasValue = true
						i++
						break
					}

					if arg.isSlice {
						arg.field.Set(reflect.Append(arg.field, reflect.ValueOf(commandline[i+1])))
					} else {
						arg.field.Set(reflect.ValueOf(commandline[i+1]))
					}

					arg.HasValue = true
					i += 2
					break
				}
			}

			if arg.HasValue {
				matched = true
				break
			}
		}

		if matched {
			continue
		}

		if strings.HasPrefix(commandline[i], "-") {
			// unrecognized argument. ignore it
			i++
		} else {
			// positional argument
			rest = append(rest, commandline[i])
			i++
		}
	}
	return
}
