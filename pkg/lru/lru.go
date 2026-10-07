package lru

import "container/list"

type entry[K comparable, V any] struct {
	key K
	val V
}

type Cache[K comparable, V any] struct {
	limit int
	order *list.List
	items map[K]*list.Element
}

func New[K comparable, V any](limit int) *Cache[K, V] {
	return &Cache[K, V]{limit: limit, order: list.New(), items: make(map[K]*list.Element)}
}

func (c *Cache[K, V]) Get(k K) (V, bool) {
	el, ok := c.items[k]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*entry[K, V]).val, true
}

func (c *Cache[K, V]) Put(k K, v V) {
	if el, ok := c.items[k]; ok {
		el.Value.(*entry[K, V]).val = v
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&entry[K, V]{key: k, val: v})
	for c.order.Len() > c.limit {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*entry[K, V]).key)
	}
}

func (c *Cache[K, V]) Remove(k K) {
	if el, ok := c.items[k]; ok {
		c.order.Remove(el)
		delete(c.items, k)
	}
}

func (c *Cache[K, V]) Len() int { return c.order.Len() }
