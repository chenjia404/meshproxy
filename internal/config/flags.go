package config

import (
	"errors"
	"flag"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// RegisterFlags binds a config struct to a FlagSet using the config's YAML field
// names as dotted CLI flags. Example:
//
//	--mode=relay+exit
//	--p2p.listen_addrs=/ip4/0.0.0.0/tcp/4001,/ip6/::/tcp/4001
//	--client.exit_selection.mode=fixed_peer
//
// Nested pointer fields are allocated on demand when one of their child flags is set.
func RegisterFlags(fs *flag.FlagSet, cfg *Config) error {
	if fs == nil {
		return errors.New("flag set must not be nil")
	}
	if cfg == nil {
		return errors.New("config must not be nil")
	}

	b := flagBinder{root: reflect.ValueOf(cfg)}
	if err := b.registerStruct(fs, reflect.TypeOf(*cfg), nil, ""); err != nil {
		return err
	}

	// Keep backward-compatible shortcuts for the most common top-level options.
	fs.Var(newBoundValue(&flagTarget{root: b.root, path: []int{fieldIndexOfType(reflect.TypeOf(*cfg), "Socks5"), fieldIndexOfType(reflect.TypeOf(cfg.Socks5), "Listen")}}, reflect.TypeOf(cfg.Socks5.Listen), false),
		"socks5",
		"alias for --socks5.listen")
	fs.Var(newBoundValue(&flagTarget{root: b.root, path: []int{fieldIndexOfType(reflect.TypeOf(*cfg), "API"), fieldIndexOfType(reflect.TypeOf(cfg.API), "Listen")}}, reflect.TypeOf(cfg.API.Listen), false),
		"api",
		"alias for --api.listen")
	fs.Var(newBoundValue(&flagTarget{root: b.root, path: []int{fieldIndexOfType(reflect.TypeOf(*cfg), "P2P"), fieldIndexOfType(reflect.TypeOf(cfg.P2P), "NoDiscovery")}}, reflect.TypeOf(cfg.P2P.NoDiscovery), true),
		"nodisc",
		"alias for --p2p.nodisc")

	return nil
}

type flagBinder struct {
	root reflect.Value
}

func (b flagBinder) registerStruct(fs *flag.FlagSet, t reflect.Type, path []int, prefix string) error {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		name, ok := yamlFlagName(sf)
		if !ok {
			continue
		}
		nextPath := append(append([]int(nil), path...), i)
		nextPrefix := prefix + name

		switch sf.Type.Kind() {
		case reflect.Struct:
			if err := b.registerStruct(fs, sf.Type, nextPath, nextPrefix+"."); err != nil {
				return err
			}
		case reflect.Ptr:
			if sf.Type.Elem().Kind() == reflect.Struct {
				if err := b.registerStruct(fs, sf.Type.Elem(), nextPath, nextPrefix+"."); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("unsupported pointer flag type for %s: %s", nextPrefix, sf.Type)
		case reflect.Slice:
			switch sf.Type.Elem().Kind() {
			case reflect.String, reflect.Bool,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				fs.Var(newBoundValue(&flagTarget{root: b.root, path: nextPath}, sf.Type, false), nextPrefix, flagUsage(nextPrefix, sf.Type))
			default:
				// Complex slices (e.g. slice of structs) are intentionally skipped from CLI binding.
			}
		default:
			fs.Var(newBoundValue(&flagTarget{root: b.root, path: nextPath}, sf.Type, sf.Type.Kind() == reflect.Bool), nextPrefix, flagUsage(nextPrefix, sf.Type))
		}
	}
	return nil
}

func flagUsage(name string, t reflect.Type) string {
	return fmt.Sprintf("override config value for %s (type %s)", name, t.String())
}

func yamlFlagName(sf reflect.StructField) (string, bool) {
	tag := sf.Tag.Get("yaml")
	if tag == "-" {
		return "", false
	}
	if tag != "" {
		if name := strings.Split(tag, ",")[0]; name != "" {
			return name, true
		}
	}
	return toSnake(sf.Name), true
}

func toSnake(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func fieldIndexOfType(t reflect.Type, fieldName string) int {
	f, ok := t.FieldByName(fieldName)
	if !ok {
		panic("field not found: " + fieldName)
	}
	return f.Index[0]
}

type flagTarget struct {
	root reflect.Value
	path []int
}

func (t *flagTarget) resolveLeaf(allocate bool) (reflect.Value, error) {
	if !t.root.IsValid() || t.root.Kind() != reflect.Ptr || t.root.IsNil() {
		return reflect.Value{}, errors.New("config root must be a non-nil pointer")
	}
	cur := t.root.Elem()
	for i, idx := range t.path {
		if !cur.IsValid() || cur.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("invalid config path at step %d", i)
		}
		field := cur.Field(idx)
		if i == len(t.path)-1 {
			return field, nil
		}
		switch field.Kind() {
		case reflect.Struct:
			cur = field
		case reflect.Ptr:
			if field.IsNil() {
				if !allocate {
					if field.Type().Elem() == reflect.TypeOf(ExitConfig{}) {
						exit := defaultExitConfig()
						cur = reflect.ValueOf(exit)
						continue
					}
					return reflect.Value{}, fmt.Errorf("config path is nil at step %d", i)
				}
				if field.Type().Elem() == reflect.TypeOf(ExitConfig{}) {
					exit := defaultExitConfig()
					field.Set(reflect.ValueOf(&exit))
				} else {
					field.Set(reflect.New(field.Type().Elem()))
				}
			}
			cur = field.Elem()
		default:
			return reflect.Value{}, fmt.Errorf("unsupported intermediate field kind %s", field.Kind())
		}
	}
	return reflect.Value{}, errors.New("empty config path")
}

func (t *flagTarget) getString() string {
	v, err := t.resolveLeaf(false)
	if err != nil || !v.IsValid() {
		return ""
	}
	return formatValue(v)
}

func (t *flagTarget) setString(input string, typ reflect.Type) error {
	v, err := t.resolveLeaf(true)
	if err != nil {
		return err
	}
	return setValue(v, input, typ)
}

type boundValue struct {
	target *flagTarget
	typ    reflect.Type
	bool   bool
}

func newBoundValue(target *flagTarget, typ reflect.Type, isBool bool) *boundValue {
	return &boundValue{target: target, typ: typ, bool: isBool}
}

func (b *boundValue) String() string {
	if b == nil || b.target == nil {
		return ""
	}
	return b.target.getString()
}

func (b *boundValue) Set(s string) error {
	if b == nil || b.target == nil {
		return errors.New("uninitialized flag value")
	}
	return b.target.setString(s, b.typ)
}

func (b *boundValue) IsBoolFlag() bool {
	return b != nil && b.bool
}

func formatValue(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		return formatValue(v.Elem())
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Slice:
		if v.Len() == 0 {
			return ""
		}
		items := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			items = append(items, formatValue(v.Index(i)))
		}
		return strings.Join(items, ",")
	default:
		return fmt.Sprint(v.Interface())
	}
}

func setValue(v reflect.Value, input string, typ reflect.Type) error {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return errors.New("nil pointer value")
		}
		return setValue(v.Elem(), input, v.Type().Elem())
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(input)
		return nil
	case reflect.Bool:
		if input == "" {
			return errors.New("bool flag requires a value")
		}
		b, err := strconv.ParseBool(input)
		if err != nil {
			return fmt.Errorf("parse bool %q: %w", input, err)
		}
		v.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if input == "" {
			return errors.New("int flag requires a value")
		}
		n, err := strconv.ParseInt(input, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("parse int %q: %w", input, err)
		}
		v.SetInt(n)
		return nil
	case reflect.Slice:
		if input == "" {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			return nil
		}
		parts := splitCSV(input)
		slice := reflect.MakeSlice(v.Type(), 0, len(parts))
		for _, part := range parts {
			elem := reflect.New(v.Type().Elem()).Elem()
			if err := setValue(elem, part, v.Type().Elem()); err != nil {
				return err
			}
			slice = reflect.Append(slice, elem)
		}
		v.Set(slice)
		return nil
	default:
		return fmt.Errorf("unsupported flag type %s", typ.String())
	}
}

func splitCSV(input string) []string {
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return []string{}
	}
	return out
}
