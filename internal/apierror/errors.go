package apierror

type PublicError struct {
	Message    string `json:"message"`
	Code       int    `json:"code"`
	ErrorClass string `json:"errorClass"`
	HTTPCode   int    `json:"httpCode"`
}

var (
	ManifestInvalid = PublicError{
		Message:    "manifest is invalid",
		Code:       422003,
		ErrorClass: "ManifestInvalid",
		HTTPCode:   422,
	}
	IdempotencyKeyInvalid = PublicError{
		Message:    "Idempotency-Key is invalid",
		Code:       400001,
		ErrorClass: "IdempotencyKeyInvalid",
		HTTPCode:   400,
	}
	FrameTooLarge = PublicError{
		Message:    "frame exceeds the allowed size",
		Code:       413001,
		ErrorClass: "FrameTooLarge",
		HTTPCode:   413,
	}
	CaptureNotFound = PublicError{
		Message:    "capture was not found",
		Code:       404001,
		ErrorClass: "CaptureNotFound",
		HTTPCode:   404,
	}
	InternalError = PublicError{
		Message:    "an internal error occurred",
		Code:       500001,
		ErrorClass: "InternalError",
		HTTPCode:   500,
	}
	ServiceUnavailable = PublicError{
		Message:    "service is temporarily unavailable",
		Code:       503001,
		ErrorClass: "ServiceUnavailable",
		HTTPCode:   503,
	}
	CaptureStorageUnavailable = PublicError{
		Message:    "capture could not be stored temporarily",
		Code:       503002,
		ErrorClass: "CaptureStorageUnavailable",
		HTTPCode:   503,
	}
)
