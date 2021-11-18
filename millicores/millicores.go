package millicores

import (
	"context"

	"github.com/cyverse-de/jex-adapter/db"
	"gopkg.in/cyverse-de/model.v4"
)

type Detector struct {
	defaultNumber float32
	db            db.Database
}

func New(db db.Database, defaultNumber float32) *Detector {
	return &Detector{
		defaultNumber: defaultNumber,
		db:            db,
	}
}

// NumberReserved scans the job to figure out the number of millicores reserved,
// and if it's not found there it uses the defaults.
func (d *Detector) NumberReserved(job *model.Job) (float32, error) {
	var reserved float32
	if job.Steps[0].Component.Container.MaxCPUCores != 0.0 {
		reserved = job.Steps[0].Component.Container.MaxCPUCores * 1000
	} else {
		reserved = d.defaultNumber
	}
	return reserved, nil
}

func (d *Detector) StoreMillicoresReserved(job *model.Job, millicoresReserved float32) error {
	context := context.Background()
	externalID := job.InvocationID
	err := d.db.SetMillicoresReserved(context, externalID, millicoresReserved)
	return err

}
