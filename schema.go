package ginopenapi

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

func schemaFromValue(
	registry huma.Registry,
	value any,
) *huma.Schema {
	if value == nil {
		return nil
	}

	return huma.SchemaFromType(
		registry,
		reflect.TypeOf(value),
	)
}
