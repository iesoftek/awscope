package diagram

type Renderer interface {
	Format() string
	Render(model Model) ([]byte, error)
}
