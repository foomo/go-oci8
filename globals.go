package oci8

/*
#include "oci8.go.h"
*/
import "C"

// noPkgConfig is a Go tag for disabling using pkg-config and using environmental settings like CGO_CFLAGS and CGO_LDFLAGS instead

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"regexp"
	"time"
	"unsafe"
)

const (
	lobBufferSize      = 4000
	useOCISessionBegin = true
	sizeOfNilPointer   = unsafe.Sizeof(unsafe.Pointer(nil))
)

type (
	// DSN is Oracle Data Source Name
	DSN struct {
		Connect                string
		Username               string
		Password               string
		prefetchRows           C.ub4
		prefetchMemory         C.ub4
		Location               *time.Location
		transactionMode        C.ub4
		enableQMPlaceholders   bool
		operationMode          C.ub4
		externalauthentication bool
	}

	// OCI8DriverStruct is Oracle driver struct
	OCI8DriverStruct struct {
		// Logger is used to log connection ping errors, defaults to discard
		// To log set it to something like: log.New(os.Stderr, "oci8 ", log.Ldate|log.Ltime|log.LUTC|log.Llongfile)
		Logger *log.Logger
	}

	// OCI8Connector is the sql driver connector
	OCI8Connector struct {
		// Logger is used to log connection ping errors
		Logger *log.Logger
	}

	// OCI8Conn is Oracle connection
	OCI8Conn struct {
		svc                  *C.OCISvcCtx
		srv                  *C.OCIServer
		env                  *C.OCIEnv
		errHandle            *C.OCIError
		usrSession           *C.OCISession
		prefetchRows         C.ub4
		prefetchMemory       C.ub4
		location             *time.Location
		transactionMode      C.ub4
		operationMode        C.ub4
		inTransaction        bool
		enableQMPlaceholders bool
		closed               bool
		logger               *log.Logger
	}

	// OCI8Tx is Oracle transaction
	OCI8Tx struct {
		conn *OCI8Conn
	}

	namedValue struct {
		Name    string
		Ordinal int
		Value   driver.Value
	}

	outValue struct {
		Dest interface{}
		In   bool
	}

	// OCI8Stmt is Oracle statement
	OCI8Stmt struct {
		conn   *OCI8Conn
		stmt   *C.OCIStmt
		closed bool
		pbind  []oci8Bind // bind params
	}

	// OCI8Result is Oracle result
	OCI8Result struct {
		rowsAffected    int64
		rowsAffectedErr error
		rowid           string
		rowidErr        error
		stmt            *OCI8Stmt
	}

	oci8Define struct {
		name         string
		dataType     C.ub2
		pbuf         unsafe.Pointer
		maxSize      C.sb4
		length       *C.ub2
		indicator    *C.sb2
		defineHandle *C.OCIDefine
	}

	oci8Bind struct {
		dataType   C.ub2
		pbuf       unsafe.Pointer
		maxSize    C.sb4
		length     *C.ub2
		indicator  *C.sb2
		bindHandle *C.OCIBind
		out        sql.Out
	}

	// OCI8Rows is Oracle rows
	OCI8Rows struct {
		stmt    *OCI8Stmt
		defines []oci8Define
		e       bool
		closed  bool
		done    chan struct{}
		cls     bool
	}
)

var (
	// ErrOCISuccessWithInfo is OCI_SUCCESS_WITH_INFO
	ErrOCISuccessWithInfo = errors.New("OCI_SUCCESS_WITH_INFO")
	// ErrNoRowid is result has no rowid
	ErrNoRowid = errors.New("result has no rowid")

	phre           = regexp.MustCompile(`\?`)
	defaultCharset = C.ub2(0)

	// OCI8Driver is the sql driver
	OCI8Driver = &OCI8DriverStruct{
		Logger: log.New(ioutil.Discard, "", 0),
	}
)

func init() {
	sql.Register("oci8", OCI8Driver)

	// set defaultCharset to AL32UTF8
	var envP *C.OCIEnv
	envPP := &envP
	var result C.sword

	result = C.OCIEnvCreate(envPP, C.OCI_DEFAULT, nil, nil, nil, nil, 0, nil)
	if result != C.OCI_SUCCESS {
		switch result {
		case C.OCI_SUCCESS_WITH_INFO:
			fmt.Printf("Error - OCI_SUCCESS_WITH_INFO\n")
			//OCIErrorGet ((void  *) errhp, (ub4) 1, (text *) NULL, &errcode, errbuf, (ub4) sizeof(errbuf), (ub4) OCI_HTYPE_ERROR);
			//fmt.Printf("Error - %s\n", errbuf)
		case C.OCI_NEED_DATA:
			fmt.Printf("Error - OCI_NEED_DATA\n")
		case C.OCI_NO_DATA:
			fmt.Printf("Error - OCI_NO_DATA\n")
		case C.OCI_ERROR:
			fmt.Printf("Error - OCI_ERROR\n")

			fmt.Println([]byte(cGoStringN(cString("test"), 4)))
			fmt.Println(cGoStringN(cString("test"), 4))

			var errbuf = cStringN("", 1024)
			fmt.Println(fmt.Sprint(C.OCIErrorGet(unsafe.Pointer(*envPP), 1, nil, &result, errbuf, 1024, C.OCI_HTYPE_ENV)))

			//errorText := make([]byte, 512)
			// fmt.Println(C.OCIErrorGet(unsafe.Pointer(*envPP), 1, nil, &result, (*C.OraText)(&errorText[0]), 512, C.OCI_HTYPE_ENV))

			var msg = cGoStringN(errbuf, 512)
			//index := bytes.IndexByte(errorText, 0)
			//var msg = string(errorText[:index])

			fmt.Println("ERROR MESSAGE:", msg)
			//fmt.Println(hex.Dump([]byte(msg)))
		case C.OCI_INVALID_HANDLE:
			fmt.Printf("Error - OCI_INVALID_HANDLE\n")
		case C.OCI_STILL_EXECUTING:
			fmt.Printf("Error - OCI_STILL_EXECUTING\n")
		case C.OCI_CONTINUE:
			fmt.Printf("Error - OCI_CONTINUE\n")
		default:
			fmt.Printf("Error - %d\n", result)
		}
		panic("OCIEnvCreate error: " + fmt.Sprint(result))
	}

	nlsLang := cString("AL32UTF8")
	defaultCharset = C.OCINlsCharSetNameToId(unsafe.Pointer(*envPP), (*C.oratext)(nlsLang))
	C.free(unsafe.Pointer(nlsLang))

	C.OCIHandleFree(unsafe.Pointer(*envPP), C.OCI_HTYPE_ENV)
}

/*
OCI Documentation Notes

Datatypes:
https://docs.oracle.com/en/database/oracle/oracle-database/12.2/lnoci/data-types.html

Handle and Descriptor Attributes:
https://docs.oracle.com/en/database/oracle/oracle-database/12.2/lnoci/handle-and-descriptor-attributes.html

OCI Function Server Round Trips:
https://docs.oracle.com/en/database/oracle/oracle-database/12.2/lnoci/oci-function-server-round-trips.html

OCI examples:
https://github.com/alexeyvo/oracle_oci_examples

Oracle datatypes:
https://ss64.com/ora/syntax-datatypes.html
*/
