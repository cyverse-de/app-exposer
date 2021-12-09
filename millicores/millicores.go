package millicores

import (
	"context"

	"github.com/cyverse-de/jex-adapter/db"
	"gopkg.in/cyverse-de/model.v4"
)

type Detector struct {
	defaultNumber float64
	db            *db.Database
}

func New(db *db.Database, defaultNumber float64) *Detector {
	return &Detector{
		defaultNumber: defaultNumber,
		db:            db,
	}
}

// NumberReserved scans the job to figure out the number of millicores reserved,
// and if it's not found there it uses the defaults.
func (d *Detector) NumberReserved(job *model.Job) (float64, error) {
	var reserved float64

	for _, step := range job.Steps {
		if step.Component.Container.MaxCPUCores != 0.0 {
			reserved = reserved + float64(step.Component.Container.MaxCPUCores*1000)
		} else {
			reserved = reserved + d.defaultNumber
		}

	}
	return reserved, nil
}

func (d *Detector) StoreMillicoresReserved(context context.Context, job *model.Job, millicoresReserved float64) error {
	externalID := job.InvocationID
	err := d.db.SetMillicoresReserved(context, externalID, millicoresReserved)
	return err

}
