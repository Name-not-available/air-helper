package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"bnb-fetcher/config"
	"bnb-fetcher/db"
	"bnb-fetcher/fetcher"
	"bnb-fetcher/filter"
	"bnb-fetcher/models"
	"bnb-fetcher/parser"
	"bnb-fetcher/scheduler"
	"bnb-fetcher/sheets"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	// Parse command line arguments
	url := flag.String("url", "", "Bnb search URL (optional, if not provided, runs as Telegram bot)")
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	maxPages := flag.Int("pages", 5, "Maximum number of pages to fetch")
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

// runCLIMode runs the fetcher in CLI mode
func runCLIMode(urlStr, configPath string, maxPages int, spreadsheetURL, credentialsPath string) {
	// Add currency=USD to URL
	urlStr = addCurrencyToURL(urlStr)

	// Load configuration
	cfg := loadConfig(configPath)

	// Perform fetching
	filteredListings, allListings, err := fetchListings(urlStr, maxPages, cfg)
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

	// Format filter information for CLI mode
	filterInfo := fmt.Sprintf("Min Reviews: %d, Min Price: %.2f, Max Price: %.2f, Min Stars: %.2f",
		cfg.Filters.MinReviews, cfg.Filters.MinPrice, cfg.Filters.MaxPrice, cfg.Filters.MinStars)

	// Create a temporary sheet name for CLI mode
	sheetName := fmt.Sprintf("CLI_%s", time.Now().Format("20060102_150405"))
	
	// Use CreateSheetAndWriteListings to insert at the beginning
	// CLI mode doesn't have unfiltered listings, so pass empty slice
	_, _, err = writer.CreateSheetAndWriteListings(sheetName, filteredListings, []models.Listing{}, urlStr, filterInfo)
	if err != nil {
		log.Printf("Warning: Failed to write to Google Sheets: %v\n", err)
	} else {
		fmt.Printf("\nSuccessfully wrote %d listings to Google Sheets\n", len(filteredListings))
	}
}

// Allowed user IDs
var allowedUserIDs = map[int64]bool{
	420478432: true,
	425120436: true,
}

// pendingConfigInput tracks which config type a user is currently entering a value for
var pendingConfigInput = make(map[int64]string)

// handleCallbackQuery handles callback queries from inline keyboard buttons
func handleCallbackQuery(bot *tgbotapi.BotAPI, database *db.DB, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID
	data := callback.Data

	// Acknowledge callback
	bot.Send(tgbotapi.NewCallback(callback.ID, ""))

	// Handle different callback types
	if strings.HasPrefix(data, "config|") {
		configType := strings.TrimPrefix(data, "config|")
		handleConfigCallback(bot, database, chatID, userID, configType, callback.Message.MessageID)
	} else if strings.HasPrefix(data, "set|") {
		// Format: set|configType|value
		parts := strings.SplitN(data, "|", 3)
		if len(parts) == 3 {
			configType := parts[1]
			valueStr := parts[2]
			handleSetConfigValue(bot, database, chatID, userID, configType, valueStr, callback.Message.MessageID)
		}
	} else if strings.HasPrefix(data, "input|") {
		// Format: input|configType
		configType := strings.TrimPrefix(data, "input|")
		// Store which config type this user is entering
		pendingConfigInput[userID] = configType
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Please enter the new value for %s (as a number):", configType)))
	}
}

// showConfigMenu shows the main config menu
func showConfigMenu(bot *tgbotapi.BotAPI, database *db.DB, chatID int64, userID int64) {
	userConfig, err := database.GetUserConfig(userID)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Error loading config: %v", err))
		bot.Send(msg)
		return
	}

	configText := fmt.Sprintf(
		"⚙️ Current Configuration:\n\n"+
			"📄 Max Pages: %d\n"+
			"⭐ Min Reviews: %d\n"+
			"💰 Min Price: %.2f\n"+
			"💰 Max Price: %.2f\n"+
			"⭐ Min Stars: %.2f\n\n"+
			"Click buttons below to change values:",
		userConfig.MaxPages, userConfig.MinReviews, userConfig.MinPrice, userConfig.MaxPrice, userConfig.MinStars)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📄 Max Pages", "config|max_pages"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ Min Reviews", "config|min_reviews"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Min Price", "config|min_price"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Max Price", "config|max_price"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ Min Stars", "config|min_stars"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, configText)
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

// handleConfigCallback shows options for changing a specific config value
func handleConfigCallback(bot *tgbotapi.BotAPI, database *db.DB, chatID int64, userID int64, configType string, messageID int) {
	userConfig, err := database.GetUserConfig(userID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Error loading config: %v", err)))
		return
	}

	var text string
	var keyboard tgbotapi.InlineKeyboardMarkup

	switch configType {
	case "max_pages":
		currentValue := userConfig.MaxPages
		text = fmt.Sprintf("📄 Max Pages\n\nCurrent: %d\n\nSelect new value or enter custom:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("3", "set|max_pages|3"),
				tgbotapi.NewInlineKeyboardButtonData("5", "set|max_pages|5"),
				tgbotapi.NewInlineKeyboardButtonData("10", "set|max_pages|10"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("15", "set|max_pages|15"),
				tgbotapi.NewInlineKeyboardButtonData("20", "set|max_pages|20"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✏️ Custom Value", "input|max_pages"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config|back"),
			),
		)
	case "min_reviews":
		currentValue := userConfig.MinReviews
		text = fmt.Sprintf("⭐ Min Reviews\n\nCurrent: %d\n\nSelect new value or enter custom:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("0", "set|min_reviews|0"),
				tgbotapi.NewInlineKeyboardButtonData("5", "set|min_reviews|5"),
				tgbotapi.NewInlineKeyboardButtonData("10", "set|min_reviews|10"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("20", "set|min_reviews|20"),
				tgbotapi.NewInlineKeyboardButtonData("50", "set|min_reviews|50"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✏️ Custom Value", "input|min_reviews"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config|back"),
			),
		)
	case "min_price":
		currentValue := userConfig.MinPrice
		text = fmt.Sprintf("💰 Min Price\n\nCurrent: %.2f\n\nSelect new value or enter custom:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("0", "set|min_price|0"),
				tgbotapi.NewInlineKeyboardButtonData("50", "set|min_price|50"),
				tgbotapi.NewInlineKeyboardButtonData("100", "set|min_price|100"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("200", "set|min_price|200"),
				tgbotapi.NewInlineKeyboardButtonData("500", "set|min_price|500"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✏️ Custom Value", "input|min_price"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config|back"),
			),
		)
	case "max_price":
		currentValue := userConfig.MaxPrice
		text = fmt.Sprintf("💰 Max Price\n\nCurrent: %.2f\n\nSelect new value or enter custom:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("500", "set|max_price|500"),
				tgbotapi.NewInlineKeyboardButtonData("1000", "set|max_price|1000"),
				tgbotapi.NewInlineKeyboardButtonData("2000", "set|max_price|2000"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("3000", "set|max_price|3000"),
				tgbotapi.NewInlineKeyboardButtonData("5000", "set|max_price|5000"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✏️ Custom Value", "input|max_price"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config|back"),
			),
		)
	case "min_stars":
		currentValue := userConfig.MinStars
		text = fmt.Sprintf("⭐ Min Stars\n\nCurrent: %.2f\n\nSelect new value or enter custom:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("0.0", "set|min_stars|0.0"),
				tgbotapi.NewInlineKeyboardButtonData("3.0", "set|min_stars|3.0"),
				tgbotapi.NewInlineKeyboardButtonData("4.0", "set|min_stars|4.0"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("4.5", "set|min_stars|4.5"),
				tgbotapi.NewInlineKeyboardButtonData("4.8", "set|min_stars|4.8"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✏️ Custom Value", "input|min_stars"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config|back"),
			),
		)
	case "back":
		showConfigMenu(bot, database, chatID, userID)
		return
	default:
		return
	}

	// Edit the message with new keyboard
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ReplyMarkup = &keyboard
	bot.Send(editMsg)
}

// handleSetConfigValue updates a config value and shows confirmation
func handleSetConfigValue(bot *tgbotapi.BotAPI, database *db.DB, chatID int64, userID int64, configType string, valueStr string, messageID int) {
	var err error
	var updateText string

	switch configType {
	case "max_pages":
		var value int
		if _, err := fmt.Sscanf(valueStr, "%d", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, &value, nil, nil, nil, nil)
		updateText = fmt.Sprintf("✅ Max Pages updated to %d", value)
	case "min_reviews":
		var value int
		if _, err := fmt.Sscanf(valueStr, "%d", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, nil, &value, nil, nil, nil)
		updateText = fmt.Sprintf("✅ Min Reviews updated to %d", value)
	case "min_price":
		var value float64
		if _, err := fmt.Sscanf(valueStr, "%f", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, nil, nil, &value, nil, nil)
		updateText = fmt.Sprintf("✅ Min Price updated to %.2f", value)
	case "max_price":
		var value float64
		if _, err := fmt.Sscanf(valueStr, "%f", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, nil, nil, nil, &value, nil)
		updateText = fmt.Sprintf("✅ Max Price updated to %.2f", value)
	case "min_stars":
		var value float64
		if _, err := fmt.Sscanf(valueStr, "%f", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, nil, nil, nil, nil, &value)
		updateText = fmt.Sprintf("✅ Min Stars updated to %.2f", value)
	default:
		bot.Send(tgbotapi.NewMessage(chatID, "Unknown config type"))
		return
	}

	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ Error updating config: %v", err)))
		return
	}

	// Show updated config
	userConfig, err := database.GetUserConfig(userID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, updateText))
		return
	}

	configText := fmt.Sprintf(
		"%s\n\n⚙️ Current Configuration:\n\n"+
			"📄 Max Pages: %d\n"+
			"⭐ Min Reviews: %d\n"+
			"💰 Min Price: %.2f\n"+
			"💰 Max Price: %.2f\n"+
			"⭐ Min Stars: %.2f\n\n"+
			"Click buttons below to change values:",
		updateText, userConfig.MaxPages, userConfig.MinReviews, userConfig.MinPrice, userConfig.MaxPrice, userConfig.MinStars)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📄 Max Pages", "config|max_pages"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ Min Reviews", "config|min_reviews"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Min Price", "config|min_price"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Max Price", "config|max_price"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ Min Stars", "config|min_stars"),
		),
	)

	// If messageID is 0, send a new message instead of editing
	if messageID == 0 {
		msg := tgbotapi.NewMessage(chatID, configText)
		msg.ReplyMarkup = keyboard
		bot.Send(msg)
	} else {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, configText)
		editMsg.ReplyMarkup = &keyboard
		bot.Send(editMsg)
	}
}

// isNumeric checks if a string is numeric
func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// handleCustomConfigInput handles when user enters a custom numeric value
func handleCustomConfigInput(bot *tgbotapi.BotAPI, database *db.DB, chatID int64, userID int64, valueStr string) {
	// Show menu to select which config to update
	text := fmt.Sprintf("You entered: %s\n\nWhich config value would you like to update?", valueStr)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📄 Max Pages", fmt.Sprintf("set|max_pages|%s", valueStr)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ Min Reviews", fmt.Sprintf("set|min_reviews|%s", valueStr)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Min Price", fmt.Sprintf("set|min_price|%s", valueStr)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Max Price", fmt.Sprintf("set|max_price|%s", valueStr)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ Min Stars", fmt.Sprintf("set|min_stars|%s", valueStr)),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

// runTelegramBot runs the fetcher as a Telegram bot
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

	// Send startup notification to admin (only once)
	adminID := int64(420478432)
	startupMsg := tgbotapi.NewMessage(adminID, "🚀 Service started successfully!")
	_, err = bot.Send(startupMsg)
	if err != nil {
		log.Printf("Warning: Failed to send startup notification to admin: %v\n", err)
	} else {
		log.Printf("Startup notification sent to admin %d\n", adminID)
	}

	// Initialize database
	database, err := db.NewDB()
	if err != nil {
		log.Fatalf("Error: Failed to initialize database: %v\n", err)
	}
	defer database.Close()
	log.Println("Database initialized successfully")

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

	// Initialize and start scheduler (browser will be created on-demand)
	sched := scheduler.NewScheduler(database, bot, writer, spreadsheetURL)
	sched.Start()
	log.Println("Scheduler started (browser will be created on-demand for each request)")
	defer sched.Stop()

	// Set up update configuration - start from latest update to skip old ones
	// Get the latest update ID first to avoid processing old updates
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60
	updateConfig.Offset = -1 // This will get only new updates

	updates := bot.GetUpdatesChan(updateConfig)

	// Create persistent keyboard
	configKeyboard := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("⚙️ Config"),
		),
	)
	configKeyboard.ResizeKeyboard = true

	// Handle updates
	for update := range updates {
		// Handle callback queries (button presses)
		if update.CallbackQuery != nil {
			userID := update.CallbackQuery.From.ID
			if !allowedUserIDs[userID] {
				// Silently ignore unauthorized users
				continue
			}

			if update.CallbackQuery.Message != nil {
				handleCallbackQuery(bot, database, update.CallbackQuery)
			}
			continue
		}

		if update.Message == nil {
			continue
		}

		userID := update.Message.From.ID

		// Check authorization first - silently ignore unauthorized users
		if !allowedUserIDs[userID] {
			// Silently ignore - don't send any messages
			continue
		}

		// Handle commands
		if update.Message.IsCommand() {
			command := update.Message.Command()

			switch command {
			case "start":

				// Initialize user config
				_, err := database.GetUserConfig(userID)
				if err != nil {
					log.Printf("Warning: Failed to initialize user config for user %d: %v\n", userID, err)
				} else {
					log.Printf("User config initialized for user %d\n", userID)
				}

				// Send welcome message with persistent keyboard
				welcomeMsg := tgbotapi.NewMessage(update.Message.Chat.ID, "Welcome! Send me a Bnb search URL to fetch listings. Results will be added to Google Sheets.")
				welcomeMsg.ReplyMarkup = configKeyboard
				bot.Send(welcomeMsg)

				// Send spreadsheet link as separate message and pin it
				spreadsheetMsg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("📊 Spreadsheet: %s", spreadsheetURL))
				sentSpreadsheetMsg, err := bot.Send(spreadsheetMsg)
				if err == nil {
					pinMsg := tgbotapi.PinChatMessageConfig{
						ChatID:              update.Message.Chat.ID,
						MessageID:           sentSpreadsheetMsg.MessageID,
						DisableNotification: false,
					}
					bot.Send(pinMsg)
				}
			case "help":
				helpText := "Commands:\n/start - Start the bot\n/help - Show this help\n/config - Configure filter settings\n\nJust send me a Bnb search URL to fetch listings! Results will be automatically added to Google Sheets."
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, helpText)
				msg.ReplyMarkup = configKeyboard
				bot.Send(msg)
			case "config":
				showConfigMenu(bot, database, update.Message.Chat.ID, userID)
			case "clear":
				// Clear the spreadsheet (write empty data)
				if err := writer.WriteListings([]models.Listing{}, true); err != nil {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("Failed to clear spreadsheet: %v", err))
					msg.ReplyMarkup = configKeyboard
					bot.Send(msg)
				} else {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "✅ Spreadsheet cleared successfully!")
					msg.ReplyMarkup = configKeyboard
					bot.Send(msg)
				}
			default:
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Unknown command. Use /help for available commands.")
				msg.ReplyMarkup = configKeyboard
				bot.Send(msg)
			}
			continue
		}

		// Handle "Config" button press (from persistent keyboard)
		if update.Message.Text == "⚙️ Config" {
			showConfigMenu(bot, database, update.Message.Chat.ID, userID)
			continue
		}

		// Handle custom config value input - check if user has a pending config input
		text := strings.TrimSpace(update.Message.Text)
		if configType, hasPending := pendingConfigInput[userID]; hasPending {
			// User is entering a value for a specific config
			if isNumeric(text) {
				// Clear the pending input
				delete(pendingConfigInput, userID)
				// Update the config value directly
				handleSetConfigValue(bot, database, update.Message.Chat.ID, userID, configType, text, 0)
			} else {
				// Invalid input, clear pending and show error
				delete(pendingConfigInput, userID)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("❌ Invalid number: %s. Please enter a valid number.", text))
				bot.Send(msg)
			}
			continue
		}

		// If no pending input, check if it's a number (might be accidental)
		// Only show menu if it's clearly not a URL
		if isNumeric(text) && !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
			// Show menu to select which config to update (fallback for when user just types a number)
			handleCustomConfigInput(bot, database, update.Message.Chat.ID, userID, text)
			continue
		}

		// Handle URL messages
		url := strings.TrimSpace(update.Message.Text)
		if url == "" {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Please send me a Bnb search URL.")
			bot.Send(msg)
			continue
		}

		// Validate URL
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Please send a valid URL starting with http:// or https://")
			bot.Send(msg)
			continue
		}

		// Add currency=USD to URL
		url = addCurrencyToURL(url)

		// Send processing message
		processingMsg := tgbotapi.NewMessage(update.Message.Chat.ID, "📝 Request received! Your request has been queued and will be processed shortly. You'll receive status updates as the scraping progresses.")
		processingMsg.ReplyMarkup = configKeyboard
		sentMsg, err := bot.Send(processingMsg)
		if err != nil {
			log.Printf("Error sending processing message: %v\n", err)
			continue
		}

		// Save request to database
		req, err := database.CreateRequest(userID, sentMsg.MessageID, url)
		if err != nil {
			log.Printf("Error creating request: %v\n", err)
			errorMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentMsg.MessageID, fmt.Sprintf("❌ Error: Failed to create request: %v", err))
			bot.Send(errorMsg)
			continue
		}

		log.Printf("Created request ID %d for user %d\n", req.ID, userID)
	}
}

// refreshEnvVars refreshes environment variables (Windows-specific)
func refreshEnvVars() {
	// On Windows, we need to refresh environment variables from the system
	// This is a workaround for PowerShell/CMD not refreshing env vars immediately
	// Try PowerShell first (more reliable), then fall back to cmd
	// On Linux/Unix systems (like Railway), environment variables are already available
	cmd := exec.Command("powershell", "-Command", "Get-ChildItem Env: | ForEach-Object { \"$($_.Name)=$($_.Value)\" }")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to cmd (Windows)
		cmd = exec.Command("cmd", "/c", "set")
		output, err = cmd.Output()
		if err != nil {
			// On Linux/Unix, env vars are already available, so this is not an error
			// Just log a debug message and continue
			log.Printf("Note: Environment variable refresh skipped (likely running on Linux/Unix)\n")
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

// fetchListings performs the fetching and filtering logic
func fetchListings(url string, maxPages int, cfg *config.FilterConfig) ([]models.Listing, []models.Listing, error) {
	// Create fetcher (using headless browser for JS-rendered content)
	rodFetcher, err := fetcher.NewRodFetcher()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create fetcher: %w", err)
	}
	defer func() {
		if err := rodFetcher.Close(); err != nil {
			log.Printf("Warning: Failed to close browser: %v\n", err)
		}
	}()
	fetcherInstance := fetcher.Fetcher(rodFetcher)

	// Fetch pages
	htmlPages, err := fetcherInstance.Fetch(url, maxPages)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching failed: %w", err)
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
		// Set page number for each listing
		pageNumber := i + 1
		for j := range listings {
			listings[j].PageNumber = pageNumber
		}
		allListings = append(allListings, listings...)
	}

	if len(allListings) == 0 {
		return nil, nil, fmt.Errorf("no listings found in the fetched HTML")
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

// addCurrencyToURL adds ?currency=USD or &currency=USD to a URL
// Always sets currency=USD, replacing any existing currency parameter
func addCurrencyToURL(urlStr string) string {
	// Parse the URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		// If parsing fails, return original URL
		log.Printf("Warning: Failed to parse URL: %v\n", err)
		return urlStr
	}

	// Always set currency=USD (will replace if it already exists)
	query := parsedURL.Query()
	query.Set("currency", "USD")
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String()
}
