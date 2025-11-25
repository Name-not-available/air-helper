package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"airbnb-scraper/config"
	"airbnb-scraper/filter"
	"airbnb-scraper/models"
	"airbnb-scraper/parser"
	"airbnb-scraper/scraper"
	"airbnb-scraper/sheets"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	// Parse command line arguments
	url := flag.String("url", "", "Airbnb search URL (optional, if not provided, runs as Telegram bot)")
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	maxPages := flag.Int("pages", 5, "Maximum number of pages to scrape")
	spreadsheetURL := flag.String("spreadsheet", "https://docs.google.com/spreadsheets/d/1FoGJ6ZzDIfFv3ZZ6_qWSn8hzEk4tlUEAT7ClQKYRmFo/edit?usp=sharing", "Google Sheets URL")
	credentialsPath := flag.String("credentials", "", "Path to Google service account credentials JSON file (or use GOOGLE_SHEETS_CREDENTIALS env var)")
	flag.Parse()

	// If URL is provided, run in CLI mode
	if *url != "" {
		runCLIMode(*url, *configPath, *maxPages, *spreadsheetURL, *credentialsPath)
		return
	}

	// Otherwise, run as Telegram bot
	runTelegramBot(*configPath, *maxPages, *spreadsheetURL, *credentialsPath)
}

// runCLIMode runs the scraper in CLI mode
func runCLIMode(url, configPath string, maxPages int, spreadsheetURL, credentialsPath string) {
	// Load configuration
	cfg := loadConfig(configPath)

	// Perform scraping
	filteredListings, allListings, err := scrapeListings(url, maxPages, cfg)
	if err != nil {
		log.Fatalf("Scraping failed: %v\n", err)
	}

	// Display results to console
	fmt.Printf("Found %d listings before filtering\n", len(allListings))
	fmt.Printf("Found %d listings after filtering\n", len(filteredListings))
	fmt.Println("---")

	if len(filteredListings) == 0 {
		fmt.Println("No listings match the filter criteria.")
		return
	}

	fmt.Println("Filtered Listings:")
	fmt.Println("==================")
	formatListingsConsole(filteredListings)

	// Write to Google Sheets
	spreadsheetID := sheets.ExtractSpreadsheetID(spreadsheetURL)
	if spreadsheetID == "" {
		log.Printf("Warning: Could not extract spreadsheet ID from URL: %s\n", spreadsheetURL)
		return
	}

	writer, err := sheets.NewWriter(spreadsheetID, credentialsPath)
	if err != nil {
		log.Printf("Warning: Failed to initialize Google Sheets writer: %v\n", err)
		return
	}

	if err := writer.WriteListings(filteredListings, true); err != nil {
		log.Printf("Warning: Failed to write to Google Sheets: %v\n", err)
	} else {
		fmt.Printf("\nSuccessfully wrote %d listings to Google Sheets\n", len(filteredListings))
	}
}

// runTelegramBot runs the scraper as a Telegram bot
func runTelegramBot(configPath string, maxPages int, spreadsheetURL, credentialsPath string) {
	// Refresh environment variables (Windows-specific)
	refreshEnvVars()

	// Get bot token from environment
	botToken := os.Getenv("AIR_KEY_TG")
	if botToken == "" {
		log.Fatalf("Error: AIR_KEY_TG environment variable is not set")
	}

	// Initialize bot
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatalf("Failed to initialize bot: %v\n", err)
	}

	log.Printf("Authorized on account %s\n", bot.Self.UserName)

	// Initialize Google Sheets writer
	spreadsheetID := sheets.ExtractSpreadsheetID(spreadsheetURL)
	if spreadsheetID == "" {
		log.Fatalf("Error: Could not extract spreadsheet ID from URL: %s\n", spreadsheetURL)
	}

	// Check if credentials are available
	credsEnv := os.Getenv("GOOGLE_SHEETS_CREDENTIALS")
	if credentialsPath == "" && credsEnv == "" {
		log.Fatalf("Error: GOOGLE_SHEETS_CREDENTIALS environment variable is not set and no credentials file path provided")
	}
	if credentialsPath == "" && credsEnv != "" {
		log.Printf("Using GOOGLE_SHEETS_CREDENTIALS from environment variable (length: %d chars)\n", len(credsEnv))
	}

	writer, err := sheets.NewWriter(spreadsheetID, credentialsPath)
	if err != nil {
		log.Fatalf("Error: Failed to initialize Google Sheets writer: %v\n", err)
	}

	log.Printf("Google Sheets writer initialized for spreadsheet: %s\n", spreadsheetID)

	// Set up update configuration
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	// Load configuration once
	cfg := loadConfig(configPath)

	// Handle updates
	for update := range updates {
		if update.Message == nil {
			continue
		}

		// Handle commands
		if update.Message.IsCommand() {
			switch update.Message.Command() {
			case "start":
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Welcome! Send me an Airbnb search URL to scrape listings. Results will be added to Google Sheets.")
				bot.Send(msg)
			case "help":
				helpText := "Commands:\n/start - Start the bot\n/help - Show this help\n\nJust send me an Airbnb search URL to scrape listings! Results will be automatically added to Google Sheets."
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, helpText)
				bot.Send(msg)
			case "clear":
				// Clear the spreadsheet (write empty data)
				if err := writer.WriteListings([]models.Listing{}, true); err != nil {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("Failed to clear spreadsheet: %v", err))
					bot.Send(msg)
				} else {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "✅ Spreadsheet cleared successfully!")
					bot.Send(msg)
				}
			default:
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Unknown command. Use /help for available commands.")
				bot.Send(msg)
			}
			continue
		}

		// Handle URL messages
		url := strings.TrimSpace(update.Message.Text)
		if url == "" {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Please send me an Airbnb search URL.")
			bot.Send(msg)
			continue
		}

		// Validate URL
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Please send a valid URL starting with http:// or https://")
			bot.Send(msg)
			continue
		}

		// Send processing message
		processingMsg := tgbotapi.NewMessage(update.Message.Chat.ID, "Processing your request... This may take a while.")
		sentMsg, _ := bot.Send(processingMsg)

		// Perform scraping
		filteredListings, allListings, scrapeErr := scrapeListings(url, maxPages, cfg)
		if scrapeErr != nil {
			errorMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentMsg.MessageID, fmt.Sprintf("Error: %v", scrapeErr))
			bot.Send(errorMsg)
			continue
		}

		// Write to Google Sheets instead of sending via Telegram
		writeErr := writer.AppendListings(filteredListings)
		if writeErr != nil {
			errorMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentMsg.MessageID, 
				fmt.Sprintf("Scraping completed: Found %d listings (before: %d), but failed to write to Google Sheets: %v", 
					len(filteredListings), len(allListings), writeErr))
			bot.Send(errorMsg)
			continue
		}

		// Send success message
		successMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentMsg.MessageID,
			fmt.Sprintf("✅ Successfully scraped and added %d listings to Google Sheets!\n\nFound %d listings before filtering.\n\nView spreadsheet: %s",
				len(filteredListings), len(allListings), spreadsheetURL))
		bot.Send(successMsg)
	}
}

// refreshEnvVars refreshes environment variables (Windows-specific)
func refreshEnvVars() {
	// On Windows, we need to refresh environment variables from the system
	// This is a workaround for PowerShell/CMD not refreshing env vars immediately
	// Try PowerShell first (more reliable), then fall back to cmd
	cmd := exec.Command("powershell", "-Command", "Get-ChildItem Env: | ForEach-Object { \"$($_.Name)=$($_.Value)\" }")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to cmd
		cmd = exec.Command("cmd", "/c", "set")
		output, err = cmd.Output()
		if err != nil {
			log.Printf("Warning: Failed to refresh environment variables: %v\n", err)
			return
		}
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := line[:idx]
			value := line[idx+1:]
			// Remove trailing \r if present
			value = strings.TrimRight(value, "\r")
			// Only set if not already set (preserve existing env vars from current process)
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}
}

// loadConfig loads configuration from file or returns defaults
func loadConfig(configPath string) *config.FilterConfig {
	var cfg *config.FilterConfig
	if _, err := os.Stat(configPath); err == nil {
		var err error
		cfg, err = config.LoadConfig(configPath)
		if err != nil {
			log.Printf("Warning: Failed to load config file: %v. Using defaults.\n", err)
			cfg = config.GetDefaultConfig()
		}
	} else {
		log.Println("Config file not found. Using default configuration.")
		cfg = config.GetDefaultConfig()
	}
	return cfg
}

// scrapeListings performs the scraping and filtering logic
func scrapeListings(url string, maxPages int, cfg *config.FilterConfig) ([]models.Listing, []models.Listing, error) {
	// Create scraper (using headless browser for JS-rendered content)
	rodScraper, err := scraper.NewRodScraper()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create scraper: %w", err)
	}
	defer func() {
		if err := rodScraper.Close(); err != nil {
			log.Printf("Warning: Failed to close browser: %v\n", err)
		}
	}()
	scraperInstance := scraper.Scraper(rodScraper)

	// Scrape pages
	htmlPages, err := scraperInstance.Scrape(url, maxPages)
	if err != nil {
		return nil, nil, fmt.Errorf("scraping failed: %w", err)
	}

	if len(htmlPages) == 0 {
		return nil, nil, fmt.Errorf("no HTML pages were collected")
	}

	// Parse listings
	parserInstance := parser.NewParser()
	var allListings []models.Listing

	for i, html := range htmlPages {
		listings, err := parserInstance.ParseHTML(html)
		if err != nil {
			log.Printf("Warning: Failed to parse page %d: %v\n", i+1, err)
			continue
		}
		allListings = append(allListings, listings...)
	}

	if len(allListings) == 0 {
		return nil, nil, fmt.Errorf("no listings found in the scraped HTML")
	}

	// Apply filters
	filterInstance := filter.NewFilter(cfg)
	filteredListings := filterInstance.ApplyFilters(allListings)

	return filteredListings, allListings, nil
}

// formatListingsConsole formats listings for console output
func formatListingsConsole(listings []models.Listing) {
	for i, listing := range listings {
		fmt.Printf("\n%d. %s\n", i+1, listing.Title)
		
		// Link
		if listing.URL != "" {
			fmt.Printf("   Link: %s\n", listing.URL)
		}
		
		// Price
		if listing.Price > 0 {
			currency := listing.Currency
			if currency == "" {
				currency = "THB" // Default fallback
			}
			// Format price with currency symbol
			switch currency {
			case "USD", "$":
				fmt.Printf("   Price: $%.2f\n", listing.Price)
			case "EUR", "€":
				fmt.Printf("   Price: €%.2f\n", listing.Price)
			case "THB", "฿":
				fmt.Printf("   Price: ฿%.0f\n", listing.Price)
			case "VND", "₫":
				fmt.Printf("   Price: ₫%.0f\n", listing.Price)
			case "GBP", "£":
				fmt.Printf("   Price: £%.2f\n", listing.Price)
			default:
				fmt.Printf("   Price: %s %.2f\n", currency, listing.Price)
			}
		} else {
			fmt.Printf("   Price: Not available\n")
		}
		
		// Rating (stars)
		if listing.Stars > 0 {
			// Display stars with full precision (no rounding)
			fmt.Printf("   Rating: %g\n", listing.Stars)
		}
		
		// Review count
		if listing.ReviewCount > 0 {
			fmt.Printf("   Review count: %d\n", listing.ReviewCount)
		}
	}
}

// formatListingsTelegram formats listings for Telegram message
func formatListingsTelegram(filteredListings, allListings []models.Listing) string {
	var sb strings.Builder
	
	sb.WriteString(fmt.Sprintf("Found %d listings before filtering\n", len(allListings)))
	sb.WriteString(fmt.Sprintf("Found %d listings after filtering\n\n", len(filteredListings)))
	
	if len(filteredListings) == 0 {
		sb.WriteString("No listings match the filter criteria.")
		return sb.String()
	}
	
	sb.WriteString("Filtered Listings:\n")
	sb.WriteString("==================\n\n")
	
	for i, listing := range filteredListings {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, listing.Title))
		
		// Link
		if listing.URL != "" {
			sb.WriteString(fmt.Sprintf("   Link: %s\n", listing.URL))
		}
		
		// Price
		if listing.Price > 0 {
			currency := listing.Currency
			if currency == "" {
				currency = "THB" // Default fallback
			}
			// Format price with currency symbol
			switch currency {
			case "USD", "$":
				sb.WriteString(fmt.Sprintf("   Price: $%.2f\n", listing.Price))
			case "EUR", "€":
				sb.WriteString(fmt.Sprintf("   Price: €%.2f\n", listing.Price))
			case "THB", "฿":
				sb.WriteString(fmt.Sprintf("   Price: ฿%.0f\n", listing.Price))
			case "VND", "₫":
				sb.WriteString(fmt.Sprintf("   Price: ₫%.0f\n", listing.Price))
			case "GBP", "£":
				sb.WriteString(fmt.Sprintf("   Price: £%.2f\n", listing.Price))
			default:
				sb.WriteString(fmt.Sprintf("   Price: %s %.2f\n", currency, listing.Price))
			}
		} else {
			sb.WriteString("   Price: Not available\n")
		}
		
		// Rating (stars)
		if listing.Stars > 0 {
			// Display stars with full precision (no rounding)
			sb.WriteString(fmt.Sprintf("   Rating: %g\n", listing.Stars))
		}
		
		// Review count
		if listing.ReviewCount > 0 {
			sb.WriteString(fmt.Sprintf("   Review count: %d\n", listing.ReviewCount))
		}
		
		sb.WriteString("\n")
	}
	
	return sb.String()
}

// splitMessage splits a message into chunks of specified size
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	
	var parts []string
	lines := strings.Split(text, "\n")
	var current strings.Builder
	
	for _, line := range lines {
		if current.Len()+len(line)+1 > maxLen {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			// If a single line is too long, split it
			if len(line) > maxLen {
				for len(line) > maxLen {
					parts = append(parts, line[:maxLen])
					line = line[maxLen:]
				}
				if len(line) > 0 {
					current.WriteString(line)
					current.WriteString("\n")
				}
			} else {
				current.WriteString(line)
				current.WriteString("\n")
			}
		} else {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	
	return parts
}


