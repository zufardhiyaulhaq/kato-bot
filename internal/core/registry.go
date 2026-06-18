package core

// Cluster identifies one kato backend the bot can target. Label is the human-facing
// button text shown in the cluster picker; it defaults to Name when empty.
type Cluster struct {
	Name  string
	Label string
}

// Registry resolves a KatoClient by cluster name. It is built once at startup from
// configuration and read-only thereafter (safe for concurrent reads).
type Registry struct {
	order   []Cluster
	clients map[string]KatoClient
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{clients: map[string]KatoClient{}}
}

// Add registers a cluster and its client, preserving insertion order. A duplicate name
// overwrites the previous client without duplicating the ordered entry.
func (r *Registry) Add(c Cluster, client KatoClient) {
	if _, exists := r.clients[c.Name]; !exists {
		r.order = append(r.order, c)
	}
	r.clients[c.Name] = client
}

// List returns the registered clusters in insertion order (a copy; safe to mutate).
func (r *Registry) List() []Cluster {
	out := make([]Cluster, len(r.order))
	copy(out, r.order)
	return out
}

// Get resolves the client for a cluster name.
func (r *Registry) Get(name string) (KatoClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}
