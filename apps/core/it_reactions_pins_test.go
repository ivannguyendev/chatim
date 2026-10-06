package main

import (
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func itReactions(st *mongostore.Store) store.Reactions { return st.Reactions() }

func itPins(st *mongostore.Store) store.Pins { return st.Pins() }
