package middleware

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

var v10 = func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Use the json tag for field names so the wire-shape `errors[].field`
	// matches what the client sent. Falling back to the Go field name
	// would force the client to translate.
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		if name, ok := fld.Tag.Lookup("json"); ok {
			n := strings.SplitN(name, ",", 2)[0]
			if n != "" && n != "-" {
				return n
			}
		}
		return fld.Name
	})
	_ = v.RegisterValidation("uuid_v7", validateUUIDv7)
	_ = v.RegisterValidation("iso639_1", validateISO639_1)
	return v
}()

// Validator returns the singleton *validator.Validate for callers that
// need to register additional struct-level validations (each later
// story may add its own).
func Validator() *validator.Validate { return v10 }

// Bind decodes the request body and runs validator/v10 in one step.
// On any failure it returns an *httperror.Error suitable for Write —
// 400 (invalid-json) for parse errors, 422 (validation) for tag
// failures.
func Bind(r *http.Request, dst any) *httperror.Error {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return httperror.BadRequest("body is empty")
		}
		return &httperror.Error{
			Type:   httperror.TypeInvalidJSON,
			Title:  "invalid json",
			Status: http.StatusBadRequest,
			Detail: err.Error(),
		}
	}
	if err := v10.Struct(dst); err != nil {
		return toFieldErrors(err)
	}
	return nil
}

func toFieldErrors(err error) *httperror.Error {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return httperror.BadRequest(err.Error())
	}
	fields := make([]httperror.FieldError, 0, len(verrs))
	for _, e := range verrs {
		fields = append(fields, httperror.FieldError{
			Field:   e.Field(),
			Message: msgFor(e),
		})
	}
	return httperror.Unprocessable(fields)
}

func msgFor(e validator.FieldError) string {
	switch e.Tag() {
	case "required":
		return "is required"
	case "uuid", "uuid_v7":
		return "must be a valid UUID"
	case "min":
		return "must be at least " + e.Param()
	case "max":
		return "must be at most " + e.Param()
	case "oneof":
		return "must be one of: " + e.Param()
	case "iso639_1":
		return "must be a 2-letter ISO 639-1 language code"
	case "email":
		return "must be a valid email address"
	default:
		return "validation failed: " + e.Tag()
	}
}

func validateUUIDv7(fl validator.FieldLevel) bool {
	parsed, err := uuid.Parse(fl.Field().String())
	return err == nil && parsed.Version() == 7
}

func validateISO639_1(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	if len(s) != 2 {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
