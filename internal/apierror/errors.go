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
	MultipartInvalid = PublicError{
		Message:    "multipart request is invalid",
		Code:       400002,
		ErrorClass: "MultipartInvalid",
		HTTPCode:   400,
	}
	FrameTooLarge = PublicError{
		Message:    "frame exceeds the allowed size",
		Code:       413001,
		ErrorClass: "FrameTooLarge",
		HTTPCode:   413,
	}
	ManifestTooLarge = PublicError{
		Message:    "manifest exceeds the allowed size",
		Code:       413002,
		ErrorClass: "ManifestTooLarge",
		HTTPCode:   413,
	}
	RequestBodyTooLarge = PublicError{
		Message:    "request body exceeds the allowed size",
		Code:       413003,
		ErrorClass: "RequestBodyTooLarge",
		HTTPCode:   413,
	}
	FrameInvalid = PublicError{
		Message:    "frame is invalid",
		Code:       422004,
		ErrorClass: "FrameInvalid",
		HTTPCode:   422,
	}
	CaptureNotFound = PublicError{
		Message:    "capture was not found",
		Code:       404001,
		ErrorClass: "CaptureNotFound",
		HTTPCode:   404,
	}
	NotFound = PublicError{
		Message:    "resource was not found",
		Code:       404002,
		ErrorClass: "NotFound",
		HTTPCode:   404,
	}
	MethodNotAllowed = PublicError{
		Message:    "method is not allowed for this resource",
		Code:       405001,
		ErrorClass: "MethodNotAllowed",
		HTTPCode:   405,
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
