package valueobject

// Category is a ticket category with its display name and unit price.
type Category struct {
	ID    string
	Name  string
	Price int64
}

// Catalog resolves a category id to its name and price. For this POC it is a
// static in-memory table; a real service would source it from the event service.
var Catalog = map[string]Category{
	"cat_vip":      {ID: "cat_vip", Name: "VIP", Price: 1500000},
	"cat_festival": {ID: "cat_festival", Name: "Festival", Price: 850000},
	"cat_tribune":  {ID: "cat_tribune", Name: "Tribune", Price: 500000},
}

// Lookup returns the category for id and whether it exists.
func Lookup(id string) (Category, bool) {
	c, ok := Catalog[id]
	return c, ok
}
