// Copyright 2020 The Moov Authors
// Use of this source code is governed by an Apache License
// license that can be found in the LICENSE file.

package imagecashletter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// https://en.wikipedia.org/wiki/Substitute_check
//
// http://www.frbservices.org
//
// The Federal Reserve Banks uses the Accredited Standards Committee X9’s Specifications (X9.100-187–2016) for
// Electronic Exchange of Check and Image Data in providing its suite of Check 21 services.
//
// Record Types
const (
	fileHeaderPos           = "01"
	cashLetterHeaderPos     = "10"
	bundleHeaderPos         = "20"
	checkDetailPos          = "25"
	checkDetailAddendumAPos = "26"
	checkDetailAddendumBPos = "27"
	checkDetailAddendumCPos = "28"
	returnDetailPos         = "31"
	returnAddendumAPos      = "32"
	returnAddendumBPos      = "33"
	returnAddendumCPos      = "34"
	returnAddendumDPos      = "35"
	imageViewDetailPos      = "50"
	imageViewDataPos        = "52"
	imageViewAnalysisPos    = "54"
	creditItemPos           = "62"
	bundleControlPos        = "70"
	routingNumberSummaryPos = "85"
	cashLetterControlPos    = "90"
	fileControlPos          = "99"
	// no longer supported by the standard
	// accountTotalsDetailPos  = "40"
	// nonHitTotalsDetailPos   = "41"
	// boxSummaryPos           = "75"
)

// Errors strings specific to parsing a Batch container
var (
	//msgFileCalculatedControlEquality = "calculated %v is out-of-balance with control %v"
	// specific messages
	msgRecordLength             = "Must be at least 80 characters and found %d"
	msgFileCashLetterInside     = "Inside of current cash letter"
	msgFileCashLetterControl    = "Cash letter control without a current cash letter"
	msgFileRoutingNumberSummary = "Routing Number Summary without a current cash letter"
	msgFileBundleOutside        = "Outside of current bundle"
	msgFileBundleInside         = "Inside of current bundle"
	msgFileBundleControl        = "Bundle control without a current bundle"
	msgFileControl              = "None or more than one file control exists"
	msgFileHeader               = "None or more than one file headers exists"
	msgUnknownRecordType        = "%s is an unknown record type"
	msgFileCashLetterID         = "%s is not unique"
	msgRecordType               = "received expecting %d"
	msgFileCreditItem           = "Credit item outside of cash letter"
)

// FileError is an error describing issues validating a file
type FileError struct {
	FieldName string
	Value     string
	Msg       string
}

func (e *FileError) Error() string {
	return fmt.Sprintf("%s %s", e.FieldName, e.Msg)
}

// Format standard of X9.37 specification used to parse file
type Format uint32

const (
	// Discover format
	Discover Format = iota
	//DSTU microformat as defined https://www.frbservices.org/assets/financial-services/check/setup/frb-x937-standards-reference.pdf
	DSTU
)

// File is an imagecashletter file
type File struct {
	// ID is a client defined string used as a reference to this record
	ID string `json:"id"`
	// FileHeader is an imagecashletter FileHeader
	Header FileHeader `json:"fileHeader"`
	// CashLetters are imagecashletter Cash Letters
	CashLetters []CashLetter `json:"cashLetters,omitempty"`
	// Bundles are imagecashletter Bundles
	Bundles []Bundle `json:"bundle,omitempty"`
	// FileControl is an imagecashletter FileControl
	Control FileControl `json:"fileControl"`
}

// NewFile constructs a file template with a FileHeader and FileControl.
func NewFile() *File {
	return &File{
		Header:  NewFileHeader(),
		Control: NewFileControl(),
	}
}

type fileHeader struct {
	Header FileHeader `json:"fileHeader"`
}

type fileControl struct {
	Control FileControl `json:"fileControl"`
}

// FileFromJSON attempts to return a *File object assuming the input is valid JSON.
//
// Callers should always check for a nil-error before using the returned file.
//
// The File returned may not be valid and callers should confirm with Validate().
// Invalid files may be rejected by other Financial Institutions or ICL tools.
func FileFromJSON(bs []byte) (*File, error) {
	if len(bs) == 0 {
		return nil, errors.New("no JSON data provided")
	}

	// read any root level fields
	var f File
	file := NewFile()
	if err := json.NewDecoder(bytes.NewReader(bs)).Decode(&f); err != nil {
		return nil, fmt.Errorf("problem reading file: %v", err)
	}
	file.ID = f.ID
	file.CashLetters = f.CashLetters
	file.Bundles = f.Bundles

	// read the FileHeader
	header := fileHeader{
		Header: file.Header,
	}
	if err := json.NewDecoder(bytes.NewReader(bs)).Decode(&header); err != nil {
		return nil, fmt.Errorf("problem reading FileHeader: %v", err)
	}
	file.Header = header.Header

	// read file control
	control := fileControl{
		Control: NewFileControl(),
	}
	if err := json.NewDecoder(bytes.NewReader(bs)).Decode(&control); err != nil {
		return nil, fmt.Errorf("problem reading FileControl: %v", err)
	}
	file.Control = control.Control

	file.setRecordTypes()

	if err := file.Create(); err != nil {
		return file, err
	}
	if err := file.Validate(); err != nil {
		return file, err
	}
	return file, nil
}

// Create creates a valid imagecashletter File
func (f *File) Create() error {
	if f == nil {
		return ErrNilFile
	}
	// Requires a valid FileHeader to build FileControl
	if err := f.Header.Validate(); err != nil {
		return err
	}

	if len(f.CashLetters) <= 0 {
		return &FileError{FieldName: "CashLetters", Value: strconv.Itoa(len(f.CashLetters)), Msg: "must have []*CashLetters to be built"}
	}

	// File Control Counts
	fileCashLetterCount := len(f.CashLetters)
	// add 2 for FileHeader/control and reset if build was called twice do to error
	fileTotalRecordCount := 2
	fileTotalItemCount := 0
	fileTotalAmount := 0
	cashLetterRecordCount := 0
	bundleRecordCount := 0
	creditIndicator := 0

	// CashLetters
	for _, cl := range f.CashLetters {
		// Validate CashLetter
		if err := cl.Validate(); err != nil {
			return err
		}
		cashLetterRecordCount = cashLetterRecordCount + 2

		fileTotalItemCount = fileTotalItemCount + len(cl.GetCreditItems())

		if len(cl.GetCreditItems()) > 0 {
			fileTotalItemCount = fileTotalItemCount + len(cl.GetCreditItems())
			creditIndicator = 1
		}

		// Bundles
		for _, b := range cl.Bundles {
			// Validate Bundle
			if err := b.Validate(); err != nil {
				return err
			}

			bundleRecordCount = bundleRecordCount + 2

			// Check Items
			for _, cd := range b.Checks {

				fileTotalItemCount = fileTotalItemCount + 1
				fileTotalItemCount = fileTotalItemCount + len(cd.CheckDetailAddendumA) + len(cd.CheckDetailAddendumB) + len(cd.CheckDetailAddendumC)
				fileTotalItemCount = fileTotalItemCount + len(cd.ImageViewDetail) + len(cd.ImageViewData) + len(cd.ImageViewAnalysis)
				fileTotalAmount = fileTotalAmount + cd.ItemAmount
			}
			// Returns Items
			for _, rd := range b.Returns {

				fileTotalItemCount = fileTotalItemCount + 1
				fileTotalItemCount = fileTotalItemCount + len(rd.ReturnDetailAddendumA) + len(rd.ReturnDetailAddendumB) + len(rd.ReturnDetailAddendumC) + len(rd.ReturnDetailAddendumD)
				fileTotalItemCount = fileTotalItemCount + len(rd.ImageViewDetail) + len(rd.ImageViewData) + len(rd.ImageViewAnalysis)
				fileTotalAmount = fileTotalAmount + rd.ItemAmount
			}
		}
	}

	fileTotalRecordCount = fileTotalRecordCount + cashLetterRecordCount + bundleRecordCount + fileTotalItemCount

	// create FileControl from calculated values
	fc := NewFileControl()
	fc.CashLetterCount = fileCashLetterCount
	fc.TotalRecordCount = fileTotalRecordCount
	fc.TotalItemCount = fileTotalItemCount
	fc.FileTotalAmount = fileTotalAmount
	// May need to pass in a FC for these values
	fc.ImmediateOriginContactName = ""
	fc.ImmediateOriginContactPhoneNumber = ""
	fc.CreditTotalIndicator = creditIndicator
	f.Control = fc
	return nil
}

// Validate validates an ICL File
func (f *File) Validate() error {
	if f == nil {
		return ErrNilFile
	}
	if err := f.CashLetterIDUnique(); err != nil {
		return err
	}
	return nil
}

// SetHeader allows for header to be built.
func (f *File) SetHeader(h FileHeader) *File {
	f.Header = h
	return f
}

// AddCashLetter appends a CashLetter to the imagecashletter.File
func (f *File) AddCashLetter(cashLetter CashLetter) []CashLetter {
	f.CashLetters = append(f.CashLetters, cashLetter)
	return f.CashLetters
}

// CashLetterIDUnique verifies multiple CashLetters in a file have a unique CashLetterID
func (f *File) CashLetterIDUnique() error {
	if f == nil || len(f.CashLetters) == 0 {
		return ErrNilFile
	}
	cashLetterID := ""
	for _, cl := range f.CashLetters {
		if cashLetterID == cl.CashLetterHeader.CashLetterID {
			msg := fmt.Sprintf(msgFileCashLetterID, cashLetterID)
			return &FileError{FieldName: "CashLetterID", Value: cl.CashLetterHeader.CashLetterID, Msg: msg}
		}
		cashLetterID = cl.CashLetterHeader.CashLetterID
	}
	return nil
}

func (f *File) setRecordTypes() {
	if f == nil {
		return
	}

	f.Header.setRecordType()
	for i := range f.CashLetters {
		f.CashLetters[i].setRecordType()
	}
	for i := range f.Bundles {
		f.Bundles[i].setRecordType()
	}
	f.Control.setRecordType()
}
