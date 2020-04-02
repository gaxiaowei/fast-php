package fastcgi

type idPool struct {
	ids chan uint16
}

//AllocID implements Client.AllocID
func (p *idPool) Alloc() uint16 {
	return <-p.ids
}

// ReleaseID implements Client.ReleaseID
func (p *idPool) Release(id uint16) {
	go func() {
		p.ids <- id
	}()
}

func newIDs(limit uint32) (p idPool) {
	if limit == 0 || limit > 65536 {
		limit = 65536
	}

	ids := make(chan uint16)

	go func(maxID uint16) {
		for i := uint16(0); i < maxID; i++ {
			ids <- i
		}

		ids <- maxID
	}(uint16(limit - 1))

	p.ids = ids

	return
}
