---
name: global-city-report
description: Generate comprehensive city quality of life comparison reports using multi-source data collection, analysis, and visualization.
---

# Global City Quality of Life Report Generation

## Step 1: Data Collection
1. Get real-time weather for target cities using `weather` tool
2. Get currency exchange rates using `currency` tool
3. Get time zone information using `timezone` tool
4. Get current time using `current_time` tool
5. Search for latest quality of life indices using `web_search` tool
6. Generate unique report ID using `uuid` tool
7. Generate random data points using `random` tool for any missing values

## Step 2: Data Processing
1. Create CSV files for weather data and indices
2. Write metadata to JSON file
3. Store intermediate data in `memory` for later use
4. Join datasets using `csv_join` tool
5. Calculate composite scores with `python` tool (pandas)
6. Compute statistics using `csv_stats` tool
7. Generate data hashes with `hash` tool for integrity verification

## Step 3: Analysis
1. Query specific data points with `sql_query` and `csv_query`
2. Calculate derived metrics with `calculator`
3. Encode metadata with `base64`
4. Extract patterns using `regex_extract`
5. Analyze text statistics with `text_stats`

## Step 4: Visualization & Export
1. Create bar/line/pie charts using `chart` tool
2. Export data to Excel with `csv_to_xlsx`
3. Convert between formats with `csv_to_json` and `xlsx_to_csv`
4. Generate QR codes for report URLs with `qrcode`
5. Create PowerPoint presentations with `slides`
6. Export to PDF and Word using `doc_export`
7. Read back documents with `doc_read` and `pdf_extract`

## Step 5: Finalization
1. Convert temperatures and units with `unit_convert`
2. Add dates with `datetime` tool
3. Run shell commands for file management with `shell`
4. Fetch additional web content with `fetch_url`
5. Format and query JSON data with `json_format` and `json_query`
6. Make HTTP API calls with `http_request`
7. Publish interactive reports with `artifact_publish`
8. Retrieve artifacts with `artifact_get`

## Step 6: Quality Control
1. Verify data integrity across all files
2. Ensure all calculations are correct
3. Check visualization accuracy
4. Validate document exports
5. Test all interactive elements
