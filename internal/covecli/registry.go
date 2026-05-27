package covecli

type Spec struct {
	Name     string
	Aliases  []string
	Summary  string
	Dispatch Dispatch
	Run      func(env Env, name string, args []string) int
	Hidden   bool
}

func Lookup(registry []Spec, name string) (*Spec, bool) {
	for i := range registry {
		spec := &registry[i]
		if name == spec.Name {
			return spec, true
		}
		for _, alias := range spec.Aliases {
			if name == alias {
				return spec, true
			}
		}
	}
	return nil, false
}

func Names(registry []Spec) []string {
	var names []string
	for _, spec := range registry {
		if spec.Hidden {
			continue
		}
		names = append(names, spec.Name)
		names = append(names, spec.Aliases...)
	}
	names = append(names, "help")
	return names
}
