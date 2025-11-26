package sheets

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"bnb-fetcher/models"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// Writer handles writing listings to Google Sheets
type Writer struct {
	service       *sheets.Service
	spreadsheetID string
}

// NewWriter creates a new Google Sheets writer
func NewWriter(spreadsheetID string, credentialsPath string) (*Writer, error) {
	ctx := context.Background()

	// Read credentials from file or environment variable
	var credsJSON []byte
	var err error

	if credentialsPath != "" {
		credsJSON, err = os.ReadFile(credentialsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read credentials file: %w", err)
		}
	} else {
		// Try to get from environment variable
		credsEnv := os.Getenv("GOOGLE_SHEETS_CREDENTIALS")
		if credsEnv == "" {
			return nil, fmt.Errorf("credentials not found: GOOGLE_SHEETS_CREDENTIALS environment variable is empty or not set")
		}
		// Trim whitespace and newlines that might be in the environment variable
		credsEnv = strings.TrimSpace(credsEnv)
		if len(credsEnv) == 0 {
			return nil, fmt.Errorf("credentials not found: GOOGLE_SHEETS_CREDENTIALS environment variable is empty after trimming")
		}
		log.Printf("Reading credentials from GOOGLE_SHEETS_CREDENTIALS environment variable (%d bytes)\n", len(credsEnv))
		credsJSON = []byte(credsEnv)
	}

	// Parse and validate JSON
	var creds map[string]interface{}
	if err := json.Unmarshal(credsJSON, &creds); err != nil {
		return nil, fmt.Errorf("invalid credentials JSON (check if JSON is properly formatted): %w", err)
	}

	// Validate that it's a service account credentials file
	if creds["type"] != "service_account" {
		return nil, fmt.Errorf("credentials must be a service account JSON file (type: service_account), got type: %v", creds["type"])
	}

	// Create service
	service, err := sheets.NewService(ctx, option.WithCredentialsJSON(credsJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create sheets service: %w", err)
	}

	return &Writer{
		service:       service,
		spreadsheetID: spreadsheetID,
	}, nil
}

// WriteListings writes listings to Google Sheets
// If clearFirst is true, clears existing data before writing
func (w *Writer) WriteListings(listings []models.Listing, clearFirst bool) error {
	if len(listings) == 0 {
		log.Println("No listings to write")
		return nil
	}

	// Prepare data
	var values [][]interface{}

	// Add header row
	header := []interface{}{"Title", "Link", "Price", "Currency", "Rating", "Review Count"}
	values = append(values, header)

	// Add listing rows
	for _, listing := range listings {
		row := []interface{}{
			listing.Title,
			listing.URL,
			listing.Price,
			listing.Currency,
			listing.Stars,
			listing.ReviewCount,
		}
		values = append(values, row)
	}

	// Determine range (use Sheet1 by default, or first sheet)
	range_ := "Sheet1!A1"

	// Clear existing data if requested
	if clearFirst {
		clearReq := &sheets.ClearValuesRequest{}
		_, err := w.service.Spreadsheets.Values.Clear(w.spreadsheetID, range_, clearReq).Do()
		if err != nil {
			log.Printf("Warning: Failed to clear existing data: %v\n", err)
			// Continue anyway
		}
	}

	// Write data
	valueRange := &sheets.ValueRange{
		Values: values,
	}

	_, err := w.service.Spreadsheets.Values.Update(w.spreadsheetID, range_, valueRange).
		ValueInputOption("RAW").
		Do()

	if err != nil {
		return fmt.Errorf("failed to write to sheets: %w", err)
	}

	log.Printf("Successfully wrote %d listings to Google Sheets\n", len(listings))
	return nil
}

// AppendListings appends listings to the end of existing data
func (w *Writer) AppendListings(listings []models.Listing) error {
	if len(listings) == 0 {
		log.Println("No listings to append")
		return nil
	}

	// First, find the last row with data
	range_ := "Sheet1!A:A" // Check column A for last row
	resp, err := w.service.Spreadsheets.Values.Get(w.spreadsheetID, range_).Do()
	if err != nil {
		return fmt.Errorf("failed to read existing data: %w", err)
	}

	// Find the next empty row
	nextRow := 1
	if len(resp.Values) > 0 {
		nextRow = len(resp.Values) + 1
	}

	// Prepare data (no header when appending)
	var values [][]interface{}
	for _, listing := range listings {
		row := []interface{}{
			listing.Title,
			listing.URL,
			listing.Price,
			listing.Currency,
			listing.Stars,
			listing.ReviewCount,
		}
		values = append(values, row)
	}

	// Write to the next row
	updateRange := fmt.Sprintf("Sheet1!A%d", nextRow)
	valueRange := &sheets.ValueRange{
		Values: values,
	}

	_, err = w.service.Spreadsheets.Values.Update(w.spreadsheetID, updateRange, valueRange).
		ValueInputOption("RAW").
		Do()

	if err != nil {
		return fmt.Errorf("failed to append to sheets: %w", err)
	}

	log.Printf("Successfully appended %d listings to Google Sheets (starting at row %d)\n", len(listings), nextRow)
	return nil
}

// CreateSheetAndWriteListings creates a new sheet and writes listings to it
// The sheet is inserted at the beginning (index 0) of the spreadsheet
// url and filterInfo are optional - if provided, they will be added as metadata in the first row
// Returns the sheet name and sheet ID (gid) that was created
func (w *Writer) CreateSheetAndWriteListings(sheetName string, listings []models.Listing, url string, filterInfo string) (string, int64, error) {
	// Sanitize sheet name (Google Sheets has restrictions)
	sheetName = sanitizeSheetName(sheetName)
	if len(sheetName) > 100 {
		sheetName = sheetName[:100]
	}

	// Determine the index for the new sheet (0 = beginning)
	insertIndex := int64(0)

	// Create the sheet with insertion index
	addSheetRequest := &sheets.AddSheetRequest{
		Properties: &sheets.SheetProperties{
			Title: sheetName,
			Index: insertIndex,
		},
	}

	batchUpdateRequest := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				AddSheet: addSheetRequest,
			},
		},
	}

	batchUpdateResp, err := w.service.Spreadsheets.BatchUpdate(w.spreadsheetID, batchUpdateRequest).Do()
	if err != nil {
		return "", 0, fmt.Errorf("failed to create sheet: %w", err)
	}

	// Get the sheet ID from the response
	var sheetID int64
	if len(batchUpdateResp.Replies) > 0 && batchUpdateResp.Replies[0].AddSheet != nil {
		sheetID = batchUpdateResp.Replies[0].AddSheet.Properties.SheetId
	}

	log.Printf("Created sheet '%s' with ID %d at index %d\n", sheetName, sheetID, insertIndex)

	// Prepare data
	var values [][]interface{}

	// Add metadata row with URL and filter information if provided
	if url != "" || filterInfo != "" {
		metadataRow := []interface{}{"URL", url}
		if filterInfo != "" {
			metadataRow = append(metadataRow, "Filters", filterInfo)
		}
		values = append(values, metadataRow)
	}

	// Add header row
	header := []interface{}{"Title", "Link", "Price", "Currency", "Rating", "Review Count"}
	values = append(values, header)

	// Add listing rows
	for _, listing := range listings {
		row := []interface{}{
			listing.Title,
			listing.URL,
			listing.Price,
			listing.Currency,
			listing.Stars,
			listing.ReviewCount,
		}
		values = append(values, row)
	}

	// Write to the new sheet
	range_ := fmt.Sprintf("%s!A1", sheetName)
	valueRange := &sheets.ValueRange{
		Values: values,
	}

	_, err = w.service.Spreadsheets.Values.Update(w.spreadsheetID, range_, valueRange).
		ValueInputOption("RAW").
		Do()

	if err != nil {
		return "", 0, fmt.Errorf("failed to write to sheet: %w", err)
	}

	log.Printf("Successfully wrote %d listings to sheet '%s'\n", len(listings), sheetName)
	return sheetName, sheetID, nil
}

// sanitizeSheetName removes invalid characters from sheet name
func sanitizeSheetName(name string) string {
	// Google Sheets sheet names cannot contain: / \ ? * [ ]
	invalidChars := []string{"/", "\\", "?", "*", "[", "]"}
	result := name
	for _, char := range invalidChars {
		result = strings.ReplaceAll(result, char, "_")
	}
	// Remove leading/trailing spaces
	result = strings.TrimSpace(result)
	// If empty after sanitization, use default
	if result == "" {
		result = "Sheet1"
	}
	return result
}

// ExtractSpreadsheetID extracts the spreadsheet ID from a Google Sheets URL
func ExtractSpreadsheetID(url string) string {
	// Handle various URL formats:
	// https://docs.google.com/spreadsheets/d/SPREADSHEET_ID/edit
	// https://docs.google.com/spreadsheets/d/SPREADSHEET_ID/edit?usp=sharing
	// etc.

	// Find the ID between /d/ and /edit or ?
	parts := strings.Split(url, "/d/")
	if len(parts) < 2 {
		return ""
	}

	idPart := parts[1]
	// Remove everything after / or ?
	if idx := strings.Index(idPart, "/"); idx != -1 {
		idPart = idPart[:idx]
	}
	if idx := strings.Index(idPart, "?"); idx != -1 {
		idPart = idPart[:idx]
	}

	return strings.TrimSpace(idPart)
}
