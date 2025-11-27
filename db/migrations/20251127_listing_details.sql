-- Migration: Add listing detail fields and reviews table
-- Date: 2025-11-27
-- Description: Adds fields for superhost, guest favorite, bedrooms, bathrooms, beds, description, house rules, newest review date to listings table
--              Creates listing_reviews table to store individual reviews separately

-- Set search path
SET search_path TO telegram_bnb_helper;

-- Add new columns to listings table
ALTER TABLE listings
ADD COLUMN IF NOT EXISTS is_superhost BOOLEAN,
ADD COLUMN IF NOT EXISTS is_guest_favorite BOOLEAN,
ADD COLUMN IF NOT EXISTS bedrooms DOUBLE PRECISION,
ADD COLUMN IF NOT EXISTS bathrooms DOUBLE PRECISION,
ADD COLUMN IF NOT EXISTS beds DOUBLE PRECISION,
ADD COLUMN IF NOT EXISTS description TEXT,
ADD COLUMN IF NOT EXISTS house_rules TEXT,
ADD COLUMN IF NOT EXISTS newest_review_date TIMESTAMP;

-- Create listing_reviews table
CREATE TABLE IF NOT EXISTS listing_reviews (
    id SERIAL PRIMARY KEY,
    listing_id INTEGER NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    date TIMESTAMP NOT NULL,
    score DOUBLE PRECISION,
    full_text TEXT NOT NULL,
    time_on_airbnb VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for better query performance
CREATE INDEX IF NOT EXISTS idx_listing_reviews_listing_id ON listing_reviews(listing_id);
CREATE INDEX IF NOT EXISTS idx_listing_reviews_date ON listing_reviews(date);

-- Add comment to document the migration
COMMENT ON COLUMN listings.is_superhost IS 'Indicates if the host is a superhost';
COMMENT ON COLUMN listings.is_guest_favorite IS 'Indicates if the listing is a guest favorite';
COMMENT ON COLUMN listings.bedrooms IS 'Number of bedrooms';
COMMENT ON COLUMN listings.bathrooms IS 'Number of bathrooms';
COMMENT ON COLUMN listings.beds IS 'Number of beds';
COMMENT ON COLUMN listings.description IS 'Full description of the listing';
COMMENT ON COLUMN listings.house_rules IS 'House rules for the listing';
COMMENT ON COLUMN listings.newest_review_date IS 'Date of the newest review';

COMMENT ON TABLE listing_reviews IS 'Stores individual reviews for listings';
COMMENT ON COLUMN listing_reviews.date IS 'Date when the review was written';
COMMENT ON COLUMN listing_reviews.score IS 'Rating score (typically 1-5)';
COMMENT ON COLUMN listing_reviews.full_text IS 'Full text of the review';
COMMENT ON COLUMN listing_reviews.time_on_airbnb IS 'How long the reviewer has been on Airbnb (e.g., "Joined in 2020")';

