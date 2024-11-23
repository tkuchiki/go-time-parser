package parsetime

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tkuchiki/go-timezone"
)

var (
	errInvalidDateTime = errors.New("Invalid date/time")
	errInvalidOffset   = errors.New("Invalid offset")
	errInvalidArgs     = errors.New("Invalid arguments")
	errInvalidTimezone = errors.New("Invalid timezone")
	reISO8601          = regexp.MustCompile(ISO8601)
	reRFC8xx1123       = regexp.MustCompile(RFC8xx1123)
	reANSIC            = regexp.MustCompile(ANSIC)
	reUS               = regexp.MustCompile(US)
)

type sortedTime struct {
	time     time.Time
	priority int
}

type sortedTimes []sortedTime

func (st sortedTimes) Len() int           { return len(st) }
func (st sortedTimes) Swap(i, j int)      { st[i], st[j] = st[j], st[i] }
func (st sortedTimes) Less(i, j int) bool { return st[i].priority < st[j].priority }

// ParseTime parses the date/time string
type ParseTime struct {
	location *time.Location
}

// NewParseTime creates a new ParseTime instance with a specified time location. The location can be
// provided in several ways based on the number and type of arguments passed.
//
// Parameters (variadic):
//   - With no arguments: Uses the current local timezone
//     -
//   - With one argument: Can be either:
//   - 1. time.Location: Uses the provided location directly
//   - 2. string: Can be:
//   - - A. Empty string: Uses the current local timezone
//   - - B. Timezone name: Loads the location using time.LoadLocation
//   - - C. Timezone abbreviation: Attempts to parse using internal timezone database
//     -
//   - With two arguments:
//   - 1. string: Name for the timezone
//   - 2. int: Offset in seconds from UTC
//
// Returns:
//   - ParseTime: A new ParseTime instance configured with the specified location
//   - error: An error if the location couldn't be determined or if invalid arguments were provided
//
// Example usage:
//
//	// Using local timezone
//	pt1, _ := NewParseTime()
//
//	// Using a specific timezone name
//	pt2, _ := NewParseTime("America/New_York")
//
//	// Using a fixed timezone
//	pt3, _ := NewParseTime("EST", -18000)  // EST = UTC-5
//
//	// Using a time.Location
//	loc := time.UTC
//	pt4, _ := NewParseTime(loc)
//
// Errors:
//   - Returns error if more than 2 arguments are provided
//   - Returns error if timezone name is invalid and not a recognized abbreviation
//   - Returns error if arguments are of invalid types
func NewParseTime(location ...interface{}) (ParseTime, error) {
	var loc *time.Location
	var err error

	switch len(location) {
	case 0:
		zone, offset := time.Now().In(time.Local).Zone()
		loc = time.FixedZone(zone, offset)
	case 1:
		switch val := location[0].(type) {
		case *time.Location:
			loc = val
		case string:
			if val == "" {
				zone, offset := time.Now().In(time.Local).Zone()
				loc = time.FixedZone(zone, offset)
			} else {
				loc, err = time.LoadLocation(val)
				if err != nil {
					tz := timezone.New()
					tzAbbrInfo, err := tz.GetTzAbbreviationInfo(val)
					if err != nil && !(isRFC2822Abbrs(val)) {
						return ParseTime{}, err
					}
					loc = time.FixedZone(val, tzAbbrInfo[0].Offset())
				}
			}
		default:
			return ParseTime{}, fmt.Errorf("Invalid type: %T", val)
		}
	case 2:
		loc = time.FixedZone(location[0].(string), location[1].(int))
	default:
		return ParseTime{}, errInvalidArgs
	}

	return ParseTime{
		location: loc,
	}, err
}

// GetLocation returns *time.Location
func (pt *ParseTime) GetLocation() *time.Location {
	return pt.location
}

// SetLocation sets *time.Location
func (pt *ParseTime) SetLocation(loc *time.Location) {
	pt.location = loc
}

// fixedZone creates a new fixed time.Location based on the zone name and offset of the provided time.Time.
// This is useful when you need to create a location that maintains the same offset regardless of daylight
// savings changes.
//
// Parameters:
//   - t: A time.Time instance from which to extract the zone name and offset
//
// Returns:
//   - *time.Location: A new time.Location instance with a fixed offset from UTC
//
// Example usage:
//
//	t := time.Now()
//	fixedLoc := fixedZone(t)
//	// If t was in EST (-05:00), fixedLoc will be a Location fixed at -5 hours from UTC
func fixedZone(t time.Time) *time.Location {
	zone, offset := t.Zone()
	return time.FixedZone(zone, offset)
}

// parseOffset attempts to parse a timezone offset string and returns a corresponding time.Location.
// It supports multiple offset formats and timezone abbreviations.
//
// The function tries to parse the input string in the following order:
//  1. "-07:00" format (RFC3339 offset format with colon)
//  2. "-0700" format (RFC3339 offset format without colon)
//  3. "MST" format (timezone abbreviation)
//
// Parameters:
//   - value: A string representing either a timezone offset (like "-07:00" or "-0700")
//     or a timezone abbreviation (like "MST")
//
// Returns:
//   - *time.Location: A time.Location instance representing the parsed offset
//   - error: An error if the string cannot be parsed in any of the supported formats
//
// Example usage:
//
//	// Using RFC3339 offset format with colon
//	loc1, _ := parseOffset("-07:00")
//
//	// Using RFC3339 offset format without colon
//	loc2, _ := parseOffset("-0700")
//
//	// Using timezone abbreviation
//	loc3, _ := parseOffset("PST")
//
// Errors:
//   - Returns errInvalidOffset if the string cannot be parsed as an offset or
//     recognized as a valid timezone abbreviation
func parseOffset(value string) (*time.Location, error) {
	var err error
	var t time.Time
	var loc *time.Location

	t, err = time.Parse("-07:00", value)
	if err == nil {
		return fixedZone(t), nil
	}

	t, err = time.Parse("-0700", value)
	if err == nil {
		return fixedZone(t), nil
	}

	_, err = time.Parse("MST", value)
	if err == nil {
		tz := timezone.New()
		tzAbbrInfo, err := tz.GetTzAbbreviationInfo(value)
		if err != nil && !(isRFC2822Abbrs(value)) {
			return loc, err
		}

		return time.FixedZone(value, tzAbbrInfo[0].Offset()), nil
	}

	return loc, errInvalidOffset
}

// toLocation converts a timezone offset string into a time.Location. It handles both
// the special case "Z" (representing UTC) and standard timezone offset formats.
//
// Parameters:
//   - offset: A string representing either:
//   - "Z" (case-insensitive) for UTC
//   - A timezone offset in formats like "-07:00", "-0700"
//   - A timezone abbreviation like "PST"
//
// Returns:
//   - *time.Location: The corresponding time.Location:
//   - time.UTC for "Z"
//   - A fixed zone location for offset strings
//   - error: An error if the offset string is invalid or cannot be parsed
//
// Example usage:
//
//	// Get UTC location
//	loc1, _ := toLocation("Z")
//	loc2, _ := toLocation("z")
//
//	// Get offset-based location
//	loc3, _ := toLocation("-07:00")
//
//	// Get abbreviation-based location
//	loc4, _ := toLocation("EST")
func toLocation(offset string) (*time.Location, error) {
	var err error
	var loc *time.Location

	if strings.ToUpper(offset) == "Z" {
		loc = time.UTC
	} else {
		loc, err = parseOffset(offset)
	}

	return loc, err
}

func twoDigitTo4DigitYear(year string) (int, error) {
	val, err := strconv.Atoi(year)
	if err != nil {
		return 0, err
	}

	if val >= 70 && val <= 99 {
		return 1900 + val, err
	}

	return 2000 + val, err
}

// dateToInt converts a date component string to its integer representation. If the date string is empty,
// it uses the current time in the specified location for the requested component.
//
// Parameters:
//   - date: String representation of a date component. Can be:
//     1. Empty string: Uses current time
//     2. Two-digit year: Converts to four-digit year
//     3. Month name: Converts to month number
//     4. Numeric string: Converts directly to integer
//   - dateType: Type of date component to parse. Valid values:
//     1. "year": Year component
//     2. "month": Month component (1-12)
//     3. "day": Day of month
//     4. "hour": Hour (0-23)
//     5. "min": Minute (0-59)
//     6. "sec": Second (0-59, defaults to 0 if date is empty)
//     7. "nsec": Nanosecond (defaults to 0 if date is empty)
//   - loc: Time location to use when getting current time
//
// Returns:
//   - int: Integer value of the date component
//   - error: Error if:
//     1. Invalid dateType provided
//     2. String cannot be converted to integer
//     3. Invalid month name provided
//
// Example usage:
//
//	loc := time.UTC
//
//	// Get current year
//	year, _ := dateToInt("", "year", loc)
//
//	// Convert two-digit year
//	year, _ := dateToInt("24", "year", loc)
//
//	// Convert month name
//	month, _ := dateToInt("January", "month", loc)
//
//	// Convert numeric string
//	day, _ := dateToInt("15", "day", loc)
func dateToInt(date string, dateType string, loc *time.Location) (int, error) {
	var err error
	var val int

	if date == "" {
		switch dateType {
		case "year":
			val = time.Now().In(loc).Year()
		case "month":
			val = int(time.Now().In(loc).Month())
		case "day":
			val = time.Now().In(loc).Day()
		case "hour":
			val = time.Now().In(loc).Hour()
		case "min":
			val = time.Now().In(loc).Minute()
		case "sec":
			if date == "" {
				val = 0
			} else {
				val = time.Now().In(loc).Second()
			}
		case "nsec":
			if date == "" {
				val = 0
			} else {
				val = time.Now().In(loc).Nanosecond()
			}
		default:
			err = errInvalidDateTime
		}
	} else {
		switch dateType {
		case "year":
			if stringLen(date) == 2 {
				return twoDigitTo4DigitYear(date)
			}
		case "month":
			if _, ok := Months[date]; ok {
				return Months[date], nil
			}
		}

		val, err = strconv.Atoi(date)
		return val, err
	}

	return val, err
}

// isOnlyDate checks if only date components (year, month, day) are provided without time components
// (hour, minute). This is useful for determining if a time string represents just a date or a full
// date-time value.
//
// Parameters:
//   - year: String representing the year component
//   - month: String representing the month component
//   - day: String representing the day component
//   - hour: String representing the hour component
//   - min: String representing the minute component
//
// Returns:
//   - bool: true if:
//   - year, month, and day are non-empty AND
//   - hour and minute are empty
//
// Example usage:
//
//	// Returns true - only date components
//	isOnlyDate("2024", "01", "15", "", "")
//
//	// Returns false - includes time components
//	isOnlyDate("2024", "01", "15", "14", "30")
//
//	// Returns false - missing date components
//	isOnlyDate("", "01", "15", "", "")
func isOnlyDate(year, month, day, hour, min string) bool {
	return year != "" && month != "" && day != "" && hour == "" && min == ""
}

func stringLen(value string) int {
	return utf8.RuneCountInString(strings.Join(strings.Fields(value), ""))
}

func to24Hour(ampm string, value int) int {
	if strings.ToUpper(ampm) == "PM" {
		return 12 + value
	}

	return value
}

// parseISO8601 parses a string in ISO8601 format and returns a time.Time value along with
// a priority value indicating how specific the match was. The function supports both date-only
// and full date-time formats, with optional timezone information.
//
// The function expects the input string to match the ISO8601 pattern defined in reISO8601.
// It handles partial matches and automatically fills in missing time components for date-only strings.
//
// Parameters:
//   - value: A string in ISO8601 format (e.g., "2024-01-15" or "2024-01-15T14:30:00Z")
//   - loc: Default time.Location to use if no timezone is specified in the input string
//
// Returns:
//   - time.Time: The parsed time value
//   - int: Priority value indicating the specificity of the match
//     (difference between input length and matched portion length)
//   - error: An error if:
//     1. Input doesn't match ISO8601 format
//     2. Any date/time component is invalid
//     3. Timezone specification is invalid
//
// Example usage:
//
//	loc := time.UTC
//
//	// Parse date only
//	t1, priority, _ := parseISO8601("2024-01-15", loc)
//	// Returns midnight (00:00:00) on 2024-01-15
//
//	// Parse date-time with timezone
//	t2, priority, _ := parseISO8601("2024-01-15T14:30:00Z", loc)
//	// Returns 14:30:00 UTC on 2024-01-15
//
//	// Parse with offset
//	t3, priority, _ := parseISO8601("2024-01-15T14:30:00-07:00", loc)
//	// Returns 14:30:00 UTC-7 on 2024-01-15
func parseISO8601(value string, loc *time.Location) (time.Time, int, error) {
	var t time.Time
	var priority int
	var err error

	group := reISO8601.FindStringSubmatch(value)

	if len(group) == 0 {
		return t, priority, errInvalidDateTime
	}

	priority = stringLen(value) - stringLen(group[0])

	var year, month, day, hour, min, sec, nsec int

	if group[8] != "" {
		loc, err = toLocation(group[8])
		if err != nil {
			return t, priority, err
		}
	}

	if len(group) == 10 && group[9] != "" {
		loc, err = toLocation(group[9])
		if err != nil {
			return t, priority, err
		}
	}

	year, err = dateToInt(group[1], "year", loc)
	if err != nil {
		return t, priority, err
	}

	month, err = dateToInt(group[2], "month", loc)
	if err != nil {
		return t, priority, err
	}

	day, err = dateToInt(group[3], "day", loc)
	if err != nil {
		return t, priority, err
	}

	// 2006-01-02 -> 2006-01-02T00:00
	if isOnlyDate(group[1], group[2], group[3], group[4], group[5]) {
		group[4] = "0"
		group[5] = "0"
	}

	hour, err = dateToInt(group[4], "hour", loc)
	if err != nil {
		return t, priority, err
	}

	min, err = dateToInt(group[5], "min", loc)
	if err != nil {
		return t, priority, err
	}

	sec, err = dateToInt(group[6], "sec", loc)
	if err != nil {
		return t, priority, err
	}

	nsec, err = dateToInt(group[7], "nsec", loc)
	if err != nil {
		return t, priority, err
	}

	return time.Date(year, time.Month(month), day, hour, min, sec, nsec, loc), priority, err
}

// ISO8601 parses ISO8601, RFC3339 date/time string
func (pt *ParseTime) ISO8601(value string) (time.Time, error) {
	t, _, err := parseISO8601(value, pt.location)
	return t, err
}

// parseRFC8xx1123 parses date strings in RFC822, RFC850, and RFC1123 formats and returns
// a time.Time value along with a priority value indicating match specificity. These formats are:
//   - RFC822: "02 Jan 06 15:04 MST"
//   - RFC850: "Monday, 02-Jan-06 15:04:05 MST"
//   - RFC1123: "Mon, 02 Jan 2006 15:04:05 MST"
//
// The function supports both date-only and full date-time formats, with optional timezone
// information. If only date components are provided, the time is set to midnight (00:00:00).
//
// Parameters:
//   - value: A string in RFC822, RFC850, or RFC1123 format
//   - loc: Default time.Location to use if no timezone is specified in the input string
//
// Returns:
//   - time.Time: The parsed time value
//   - int: Priority value indicating the specificity of the match
//     (difference between input length and matched portion length)
//   - error: An error if:
//     1. Input doesn't match any of the supported RFC formats
//     2. Any date/time component is invalid
//     3. Timezone specification is invalid
//
// Example usage:
//
//	loc := time.UTC
//
//	// Parse RFC822
//	t1, priority, _ := parseRFC8xx1123("02 Jan 06 15:04 MST", loc)
//
//	// Parse RFC850
//	t2, priority, _ := parseRFC8xx1123("Monday, 02-Jan-06 15:04:05 MST", loc)
//
//	// Parse RFC1123
//	t3, priority, _ := parseRFC8xx1123("Mon, 02 Jan 2006 15:04:05 MST", loc)
//
//	// Parse date-only (sets time to 00:00:00)
//	t4, priority, _ := parseRFC8xx1123("02 Jan 06", loc)
func parseRFC8xx1123(value string, loc *time.Location) (time.Time, int, error) {
	var t time.Time
	var priority int
	var err error

	group := reRFC8xx1123.FindStringSubmatch(value)

	if len(group) == 0 {
		return t, priority, errInvalidDateTime
	}

	priority = stringLen(value) - stringLen(group[0])

	var year, month, day, hour, min, sec, nsec int

	if group[8] != "" {
		loc, err = toLocation(group[8])
		if err != nil {
			return t, priority, err
		}
	}

	day, err = dateToInt(group[1], "day", loc)
	if err != nil {
		return t, priority, err
	}

	month, err = dateToInt(group[2], "month", loc)
	if err != nil {
		return t, priority, err
	}

	year, err = dateToInt(group[3], "year", loc)
	if err != nil {
		return t, priority, err
	}

	// 02-Jan-06 -> 02-Jan-06 00:00
	if isOnlyDate(group[1], group[2], group[3], group[4], group[5]) {
		group[4] = "0"
		group[5] = "0"
	}

	hour, err = dateToInt(group[4], "hour", loc)
	if err != nil {
		return t, priority, err
	}

	min, err = dateToInt(group[5], "min", loc)
	if err != nil {
		return t, priority, err
	}

	sec, err = dateToInt(group[6], "sec", loc)
	if err != nil {
		return t, priority, err
	}

	nsec, err = dateToInt(group[7], "nsec", loc)
	if err != nil {
		return t, priority, err
	}

	return time.Date(year, time.Month(month), day, hour, min, sec, nsec, loc), priority, err
}

// RFC8xx1123 parses a date/time string in RFC822, RFC850, or RFC1123 format using the
// ParseTime instance's location. This is a convenience wrapper around parseRFC8xx1123
// that omits the priority value from the return.
//
// Supported formats:
//   - RFC822: "02 Jan 06 15:04 MST"
//   - RFC850: "Monday, 02-Jan-06 15:04:05 MST"
//   - RFC1123: "Mon, 02 Jan 2006 15:04:05 MST"
//
// Parameters:
//   - value: A string in RFC822, RFC850, or RFC1123 format
//
// Returns:
//   - time.Time: The parsed time value
//   - error: An error if the string cannot be parsed in any of the supported formats
//
// Example usage:
//
//	pt, _ := NewParseTime("America/New_York")
//
//	// Parse RFC822
//	t1, _ := pt.RFC8xx1123("02 Jan 06 15:04 EST")
//
//	// Parse RFC850
//	t2, _ := pt.RFC8xx1123("Monday, 02-Jan-06 15:04:05 EST")
//
//	// Parse RFC1123
//	t3, _ := pt.RFC8xx1123("Mon, 02 Jan 2006 15:04:05 EST")
func (pt *ParseTime) RFC8xx1123(value string) (time.Time, error) {
	t, _, err := parseRFC8xx1123(value, pt.location)
	return t, err
}

// parseANSIC parses a date string in ANSI C format (example: "Mon Jan _2 15:04:05 2006") and returns
// a time.Time value along with a priority value indicating match specificity. The function supports
// optional timezone information.
//
// The ANSI C format follows these rules:
//   - Month name (e.g., "Jan", "January")
//   - Day of month (space-padded for single digits)
//   - Hour:Minute:Second (24-hour format)
//   - Optional nanoseconds
//   - Optional timezone
//   - Year (4 digits)
//
// Parameters:
//   - value: A string in ANSI C format
//   - loc: Default time.Location to use if no timezone is specified in the input string
//
// Returns:
//   - time.Time: The parsed time value
//   - int: Priority value indicating the specificity of the match
//     (difference between input length and matched portion length)
//   - error: An error if:
//   - Input doesn't match ANSI C format
//   - Any date/time component is invalid
//   - Timezone specification is invalid
//
// Example usage:
//
//	loc := time.UTC
//
//	// Basic ANSI C format
//	t1, priority, _ := parseANSIC("Jan  2 15:04:05 2006", loc)
//
//	// With timezone
//	t2, priority, _ := parseANSIC("Jan  2 15:04:05 MST 2006", loc)
//
//	// With nanoseconds
//	t3, priority, _ := parseANSIC("Jan  2 15:04:05.999999999 2006", loc)
func parseANSIC(value string, loc *time.Location) (time.Time, int, error) {
	var t time.Time
	var err error
	var priority int

	group := reANSIC.FindStringSubmatch(value)

	if len(group) == 0 {
		return t, priority, errInvalidDateTime
	}

	priority = stringLen(value) - stringLen(group[0])

	var year, month, day, hour, min, sec, nsec int

	if group[7] != "" {
		loc, err = toLocation(group[7])
		if err != nil {
			return t, priority, err
		}
	}

	month, err = dateToInt(group[1], "month", loc)
	if err != nil {
		return t, priority, err
	}

	day, err = dateToInt(group[2], "day", loc)
	if err != nil {
		return t, priority, err
	}

	hour, err = dateToInt(group[3], "hour", loc)
	if err != nil {
		return t, priority, err
	}

	min, err = dateToInt(group[4], "min", loc)
	if err != nil {
		return t, priority, err
	}

	sec, err = dateToInt(group[5], "sec", loc)
	if err != nil {
		return t, priority, err
	}

	nsec, err = dateToInt(group[6], "nsec", loc)
	if err != nil {
		return t, priority, err
	}

	year, err = dateToInt(group[8], "year", loc)
	if err != nil {
		return t, priority, err
	}

	return time.Date(year, time.Month(month), day, hour, min, sec, nsec, loc), priority, err
}

// ANSIC parses a date/time string in ANSI C format using the ParseTime instance's location.
// This is a convenience wrapper around parseANSIC that omits the priority value from the return.
//
// The ANSI C format is: "Mon Jan _2 15:04:05 2006"
// Format components:
//   - Weekday name (optional)
//   - Month name
//   - Day of month (space-padded for single digits)
//   - Hour:Minute:Second (24-hour format)
//   - Optional nanoseconds
//   - Optional timezone
//   - Year (4 digits)
//
// Parameters:
//   - value: A string in ANSI C format
//
// Returns:
//   - time.Time: The parsed time value
//   - error: An error if the string cannot be parsed in ANSI C format
//
// Example usage:
//
//	pt, _ := NewParseTime("America/New_York")
//
//	// Basic format
//	t1, _ := pt.ANSIC("Jan  2 15:04:05 2006")
//
//	// With timezone
//	t2, _ := pt.ANSIC("Jan  2 15:04:05 EST 2006")
//
//	// With nanoseconds
//	t3, _ := pt.ANSIC("Jan  2 15:04:05.123456789 2006")
func (pt *ParseTime) ANSIC(value string) (time.Time, error) {
	t, _, err := parseANSIC(value, pt.location)
	return t, err
}

// parseUS parses a date string in US format (e.g., "Jan 2, 2006" or "January 2, 2006 3:04:05 PM MST")
// and returns a time.Time value along with a priority value indicating match specificity.
//
// The function supports multiple variations of US date format:
//   - Date only: "Jan 2, 2006" or "January 2, 2006"
//   - With time: "Jan 2, 2006 3:04:05 PM"
//   - With timezone: "Jan 2, 2006 3:04:05 PM MST"
//   - With nanoseconds: "Jan 2, 2006 3:04:05.999999999 PM MST"
//
// If only date components are provided, the time is set to midnight (00:00:00).
// Times can be specified in either 12-hour (with AM/PM) or 24-hour format.
//
// Parameters:
//   - value: A string in US date format
//   - loc: Default time.Location to use if no timezone is specified in the input string
//
// Returns:
//   - time.Time: The parsed time value
//   - int: Priority value indicating the specificity of the match
//     (difference between input length and matched portion length)
//   - error: An error if:
//     1. Input doesn't match US date format
//     2. Any date/time component is invalid
//     3. Timezone specification is invalid
//
// Example usage:
//
//	loc := time.UTC
//
//	// Parse date only
//	t1, priority, _ := parseUS("Jan 2, 2006", loc)
//
//	// Parse with time
//	t2, priority, _ := parseUS("January 2, 2006 3:04:05 PM", loc)
//
//	// Parse with timezone
//	t3, priority, _ := parseUS("Jan 2, 2006 3:04:05 PM MST", loc)
//
//	// Parse with nanoseconds
//	t4, priority, _ := parseUS("Jan 2, 2006 3:04:05.123456789 PM MST", loc)
func parseUS(value string, loc *time.Location) (time.Time, int, error) {
	var t time.Time
	var priority int
	var err error

	group := reUS.FindStringSubmatch(value)

	if len(group) == 0 {
		return t, priority, errInvalidDateTime
	}

	priority = stringLen(value) - stringLen(group[0])

	var year, month, day, hour, min, sec, nsec int

	if group[9] != "" {
		loc, err = toLocation(group[9])
		if err != nil {
			return t, priority, err
		}
	}

	month, err = dateToInt(group[1], "month", loc)
	if err != nil {
		return t, priority, err
	}

	day, err = dateToInt(group[2], "day", loc)
	if err != nil {
		return t, priority, err
	}

	year, err = dateToInt(group[3], "year", loc)
	if err != nil {
		return t, priority, err
	}

	if isOnlyDate(group[1], group[2], group[3], group[4], group[5]) {
		group[4] = "0"
		group[5] = "0"
	}

	hour, err = dateToInt(group[4], "hour", loc)
	if err != nil {
		return t, priority, err
	}

	min, err = dateToInt(group[5], "min", loc)
	if err != nil {
		return t, priority, err
	}

	sec, err = dateToInt(group[6], "sec", loc)
	if err != nil {
		return t, priority, err
	}

	nsec, err = dateToInt(group[7], "nsec", loc)
	if err != nil {
		return t, priority, err
	}

	ampm := group[8]

	if ampm != "" {
		hour = to24Hour(ampm, hour)
	}

	return time.Date(year, time.Month(month), day, hour, min, sec, nsec, loc), priority, err
}

// US parses a date/time string in US format using the ParseTime instance's location.
// This is a convenience wrapper around parseUS that omits the priority value from the return.
//
// Supported formats include:
//   - Date only: "Jan 2, 2006" or "January 2, 2006" or "01/02/2006"
//   - With time: "Jan 2, 2006 3:04:05 PM"
//   - With timezone: "Jan 2, 2006 3:04:05 PM MST"
//   - With nanoseconds: "Jan 2, 2006 3:04:05.999999999 PM MST"
//
// The function handles both 12-hour (with AM/PM) and 24-hour time formats.
// When only date is provided, time is set to midnight (00:00:00).
//
// Parameters:
//   - value: A string in US date format
//
// Returns:
//   - time.Time: The parsed time value
//   - error: An error if the string cannot be parsed in US format
//
// Example usage:
//
//	pt, _ := NewParseTime("America/New_York")
//
//	// Parse date only
//	t1, _ := pt.US("Jan 2, 2006")
//
//	// Parse with time (12-hour format)
//	t2, _ := pt.US("January 2, 2006 3:04:05 PM")
//
//	// Parse with timezone
//	t3, _ := pt.US("Jan 2, 2006 15:04:05 EST")
func (pt *ParseTime) US(value string) (time.Time, error) {
	t, _, err := parseUS(value, pt.location)
	return t, err
}

// Parse attempts to parse a date/time string using multiple format parsers, returning the most
// appropriate match. It tries the following formats in order:
//  1. ISO8601 (e.g., "2006-01-02T15:04:05Z")
//  2. RFC822/RFC850/RFC1123 (e.g., "Mon, 02 Jan 2006 15:04:05 MST")
//  3. ANSI C (e.g., "Mon Jan _2 15:04:05 2006")
//  4. US format (e.g., "Jan 2, 2006 3:04:05 PM MST")
//
// The function uses a priority system to determine the best match when multiple formats
// are valid for the input string. The priority is based on how closely the input matches
// each format pattern, with lower priority values indicating better matches.
//
// Parameters:
//   - value: A date/time string in any of the supported formats
//
// Returns:
//   - time.Time: The parsed time value from the best matching format
//   - error: errInvalidDateTime if the string cannot be parsed in any supported format
//
// Example usage:
//
//	pt, _ := NewParseTime("America/New_York")
//
//	// ISO8601 format
//	t1, _ := pt.Parse("2006-01-02T15:04:05Z")
//
//	// RFC1123 format
//	t2, _ := pt.Parse("Mon, 02 Jan 2006 15:04:05 MST")
//
//	// ANSI C format
//	t3, _ := pt.Parse("Jan  2 15:04:05 2006")
//
//	// US format
//	t4, _ := pt.Parse("Jan 2, 2006 3:04:05 PM")
//
// Note: If a string is valid in multiple formats, the format with the closest match
// (lowest priority value) will be used to parse the final time value.
func (pt *ParseTime) Parse(value string) (time.Time, error) {
	times := make(sortedTimes, 0)
	t, priority, _ := parseISO8601(value, pt.location)
	if !t.IsZero() {
		times = append(times, sortedTime{time: t, priority: priority})
	}

	t, priority, _ = parseRFC8xx1123(value, pt.location)
	if !t.IsZero() {
		times = append(times, sortedTime{time: t, priority: priority})
	}

	t, priority, _ = parseANSIC(value, pt.location)
	if !t.IsZero() {
		times = append(times, sortedTime{time: t, priority: priority})
	}

	t, priority, _ = parseUS(value, pt.location)
	if !t.IsZero() {
		times = append(times, sortedTime{time: t, priority: priority})
	}

	if len(times) == 0 {
		var tmpT time.Time
		return tmpT, errInvalidDateTime
	}

	sort.Sort(times)

	return times[0].time, nil
}

func isRFC2822Abbrs(abbr string) bool {
	return abbr == "EST" || abbr == "EDT" || abbr == "CST" || abbr == "CDT" || abbr == "MST" || abbr == "MDT" || abbr == "PST" || abbr == "PDT"
}
