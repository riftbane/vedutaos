//go:build !linux

package wifi

// Start returns a manager that finds no wireless device: only the console runs one.
func Start(o Options) *Manager {
	o.defaults()
	return &Manager{o: o}
}

type session struct{}

func (s *session) scan()               {}
func (s *session) join(string, string) {}
