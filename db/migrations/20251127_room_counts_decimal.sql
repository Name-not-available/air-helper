-- Migration: Convert room count columns to DOUBLE PRECISION to support decimal values
-- Date: 2025-11-27

SET search_path TO telegram_bnb_helper;

ALTER TABLE listings
    ALTER COLUMN bedrooms TYPE DOUBLE PRECISION USING bedrooms::DOUBLE PRECISION,
    ALTER COLUMN bathrooms TYPE DOUBLE PRECISION USING bathrooms::DOUBLE PRECISION,
    ALTER COLUMN beds TYPE DOUBLE PRECISION USING beds::DOUBLE PRECISION;

COMMENT ON COLUMN listings.bedrooms IS 'Number of bedrooms (supports decimals)';
COMMENT ON COLUMN listings.bathrooms IS 'Number of bathrooms (supports decimals)';
COMMENT ON COLUMN listings.beds IS 'Number of beds (supports decimals)';

