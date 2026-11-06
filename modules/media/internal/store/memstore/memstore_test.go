package memstore_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/store"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
)

func TestUploadsContract(t *testing.T) {
	storetest.RunUploads(t, func(*testing.T) store.Uploads { return memstore.NewUploads() })
}
