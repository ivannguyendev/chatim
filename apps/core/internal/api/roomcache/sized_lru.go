package roomcache

import "container/list"

type sizedEntry[K comparable, V any] struct {
	key  K
	val  V
	size int
}

type sizedLRU[K comparable, V any] struct {
	limit int
	used  int
	order *list.List
	items map[K]*list.Element
}

func newSizedLRU[K comparable, V any](limit int) *sizedLRU[K, V] {
	return &sizedLRU[K, V]{limit: limit, order: list.New(), items: make(map[K]*list.Element)}
}

func (c *sizedLRU[K, V]) get(k K) (V, bool) {
	el, ok := c.items[k]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*sizedEntry[K, V]).val, true
}

func (c *sizedLRU[K, V]) put(k K, v V, size int) bool {
	c.remove(k)
	if size > c.limit {
		return false
	}
	c.items[k] = c.order.PushFront(&sizedEntry[K, V]{key: k, val: v, size: size})
	c.used += size
	for c.used > c.limit {
		c.drop(c.order.Back())
	}
	return true
}

func (c *sizedLRU[K, V]) remove(k K) {
	if el, ok := c.items[k]; ok {
		c.drop(el)
	}
}

func (c *sizedLRU[K, V]) drop(el *list.Element) {
	e := el.Value.(*sizedEntry[K, V])
	c.order.Remove(el)
	delete(c.items, e.key)
	c.used -= e.size
}

func (c *sizedLRU[K, V]) bytes() int { return c.used }

func (c *sizedLRU[K, V]) entries() int { return c.order.Len() }
