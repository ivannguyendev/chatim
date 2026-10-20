package mentionapp

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

func SetBootstrap(a *App, f func(context.Context, *mongo.Database) error) { a.bootstrap = f }
