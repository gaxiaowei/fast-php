package fastcgi

type recType uint8

const version  uint8 = 1

const (
	typeBeginRequest    recType = 1
	typeAbortRequest    recType = 2
	typeEndRequest      recType = 3
	typeParams          recType = 4
	typeStdin           recType = 5
	typeStdout          recType = 6
	typeStderr          recType = 7
	typeData            recType = 8
	typeGetValues       recType = 9
	typeGetValuesResult recType = 10
	typeUnknownType     recType = 11
)

func (t recType) String() string {
	switch t {
	case typeBeginRequest:
		return "FCGI_BEGIN_REQUEST"

	case typeAbortRequest:
		return "FCGI_BEGIN_REQUEST"

	case typeEndRequest:
		return "FCGI_END_REQUEST"

	case typeParams:
		return "FCGI_PARAMS"

	case typeStdin:
		return "FCGI_STDIN"

	case typeStdout:
		return "FCGI_STDOUT"

	case typeStderr:
		return "FCGI_STDERR"

	case typeData:
		return "FCGI_DATA"

	case typeGetValues:
		return "FCGI_GET_VALUES"

	case typeGetValuesResult:
		return "FCGI_GET_VALUES_RESULT"

	case typeUnknownType:
		fallthrough

	default:
		return "FCGI_UNKNOWN_TYPE"
	}
}

func (t recType) GoString() string {
	return t.String()
}

const (
	//maximum record body
	maxWrite = 65535

	maxPad = 255
)

const (
	//role type
	RoleResponder uint16 = iota + 1
)

const (
	//service status
	statusRequestComplete = iota
	statusCantMultiplex
	statusOverloaded
	statusUnknownRole
)
