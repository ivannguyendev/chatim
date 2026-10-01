package actor

import "container/list"

type lruEntry[K comparable, V any] struct {
	key K
	val V
}

type lru[K comparable, V any] struct {
	limit int
	order *list.List
	items map[K]*list.Element
}

func newLRU[K comparable, V any](limit int) *lru[K, V] {
	return &lru[K, V]{limit: limit, order: list.New(), items: make(map[K]*list.Element)}
}

func (c *lru[K, V]) get(k K) (V, bool) {
	el, ok := c.items[k]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*lruEntry[K, V]).val, true
}

func (c *lru[K, V]) put(k K, v V) {
	if el, ok := c.items[k]; ok {
		el.Value.(*lruEntry[K, V]).val = v
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&lruEntry[K, V]{key: k, val: v})
	for c.order.Len() > c.limit {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*lruEntry[K, V]).key)
	}
}

func (c *lru[K, V]) len() int { return c.order.Len() }
