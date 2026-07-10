package util

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"

	"github.com/gocolly/colly"
	"go.uber.org/zap"
)

const RESULTS_PER_PAGE = 16

type ScraperConfig struct {
	Timeout     time.Duration
	Retries     int
	Concurrency int
	UserAgent   string
}

var scraper = struct {
	mu  sync.RWMutex
	cfg ScraperConfig
	sem chan struct{}
}{
	cfg: ScraperConfig{
		Timeout:     15 * time.Second,
		Retries:     2,
		Concurrency: 4,
		UserAgent:   "Cedarline Reforger Mods API/1.0 (+https://cedarline.digital)",
	},
	sem: make(chan struct{}, 4),
}

func ConfigureScraper(cfg ScraperConfig) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "Cedarline Reforger Mods API/1.0 (+https://cedarline.digital)"
	}
	scraper.mu.Lock()
	defer scraper.mu.Unlock()
	scraper.cfg = cfg
	scraper.sem = make(chan struct{}, cfg.Concurrency)
}

// scrapes multiple mods from a given workshop page
func ScrapeMods(pageNumber int, search string, sort string, tags []string) (*models.WebScrapeResults, error) {
	return ScrapeModsContext(context.Background(), pageNumber, search, sort, tags)
}

func ScrapeModsContext(ctx context.Context, pageNumber int, search string, sort string, tags []string) (*models.WebScrapeResults, error) {
	if err := acquireScraper(ctx); err != nil {
		return nil, err
	}
	defer releaseScraper()

	if sort == "" {
		sort = "popularity" // if no sort option is given defualt to popularity
	}
	var baseURL string = "reforger.armaplatform.com"
	workshopURL := fmt.Sprintf("https://%s/workshop?page=%d&search=%s&sort=%s", baseURL, pageNumber, url.QueryEscape(search), url.QueryEscape(sort))
	var mods []models.ModPreview

	for i := 0; i < len(tags); i++ {
		fmt.Printf("%d\n", i)
		workshopURL = workshopURL + fmt.Sprintf("&tags=%s", strings.ToUpper(tags[i]))
	}

	c := newCollector(baseURL)

	// Mod results
	var names []string
	var authors []string
	var imageURLs []string
	var modURLs []string
	var sizes []string
	var ratings []string

	// Meta info
	var totalPages int
	var resultSummary string
	var totalMods int
	var shownMods int
	var modsIndexStart int
	var modsIndexEnd int

	// Check if nothing found (i.e query has no matches or page number > total pages)
	c.OnHTML("div.container div.flex div.grid div.text-center", func(e *colly.HTMLElement) {
		resultSummary = e.Text
	})

	// Mod names
	c.OnHTML("div.grid h2.break-words", func(e *colly.HTMLElement) {
		names = append(names, e.Text)
		// fmt.Printf("Name -> %s\n", e.Text)
	})

	// Mod authors
	c.OnHTML("div.grid span.mt-1", func(e *colly.HTMLElement) {
		authors = append(authors, e.Text[3:])
		// fmt.Printf("Author -> %s\n", e.Text[3:])
	})

	// Mod image URLs
	c.OnHTML("div.grid div.aspect-h-9", func(e *colly.HTMLElement) {
		// fmt.Printf("Image URL -> %s\n", fmt.Sprintf("%s&w=1080&q=100", strings.Split(strings.Split(e.Text, "srcSet=\"")[1], "&")[0]))
		// The cover image URL is inside the card's noscript fallback markup,
		// which colly exposes as text: newer Workshop pages use src=, older
		// ones srcSet=.
		var imageURL string
		if strings.Contains(e.Text, "srcSet=\"") {
			imageURL = resolveWorkshopURL(baseURL, strings.Split(strings.Split(e.Text, "srcSet=\"")[1], "&")[0])
			if imageURL != "" {
				imageURL = fmt.Sprintf("%s&w=1080&q=100", imageURL)
			}
		} else if strings.Contains(e.Text, "src=\"") {
			imageURL = resolveWorkshopURL(baseURL, strings.Split(strings.Split(e.Text, "src=\"")[1], "\"")[0])
		}
		if imageURL == "" || strings.HasPrefix(imageURL, "data:") {
			// Placeholder as used on reforger.armaplatform.com/workshop for
			// mods with no image
			imageURL = "https://via.placeholder.com/640x360"
		}
		imageURLs = append(imageURLs, imageURL)
	})

	// Mod URLs
	c.OnHTML("div.grid a.group[href]", func(e *colly.HTMLElement) {
		modURLs = append(modURLs, resolveWorkshopURL(baseURL, e.Attr("href")))
		// fmt.Printf("Mod URL -> %s\n", e.Attr("href"))
	})

	// Mod sizes + ratings
	c.OnHTML("div.grid span.ml-1", func(e *colly.HTMLElement) {
		if !strings.Contains(e.Text, "%") {
			sizes = append(sizes, e.Text)
			// fmt.Printf("Size -> %s\n", e.Text)
		} else {
			ratings = append(ratings, e.Text)
			// fmt.Printf("Rating -> %s\n", e.Text)
		}
	})

	// Result summray + total pages
	c.OnHTML("div.flex div.hidden p.text-sm", func(e *colly.HTMLElement) {
		resultSummary = e.Text
		totalResultsStr := strings.Split(strings.Split(e.Text, "of ")[1], " results")[0]
		totalResults, _ := strconv.Atoi(totalResultsStr)
		// This is dumb but works, convert ints to float64s to divide and use math.ceil then convert back to int
		totalPages = int(math.Ceil(float64(totalResults) / float64(RESULTS_PER_PAGE)))
	})

	if err := c.Visit(workshopURL); err != nil {
		return nil, err
	}

	if resultSummary == "No mods found." {
		return &models.WebScrapeResults{
			Found: false,
		}, nil
	} else {
		shownMods = len(names)

		// This looks awful but oh whell

		var err error
		totalMods, err = strconv.Atoi(strings.Split(resultSummary, " ")[5])
		if err != nil {
			return &models.WebScrapeResults{}, err
		}

		modsIndexStart, err = strconv.Atoi(strings.Split(resultSummary, " ")[1])
		if err != nil {
			return &models.WebScrapeResults{}, err
		}

		modsIndexEnd, err = strconv.Atoi(strings.Split(resultSummary, " ")[3])
		if err != nil {
			return &models.WebScrapeResults{}, err
		}
	}

	// Create mod structs from results
	for i := 0; i < len(names); i++ {
		modID := strings.Split(strings.Split(modURLs[i], "/")[4], "-")[0]
		mods = append(mods, models.ModPreview{
			Name:           names[i],
			Author:         authors[i],
			ImageURL:       imageURLs[i],
			OriginalModURL: modURLs[i],
			APIModURL:      fmt.Sprintf("%s/v1/mod/%s", config.GetFullURL(), modID),
			Size:           sizes[i],
			Rating:         ratings[i],
			ID:             modID,
		})
	}

	return &models.WebScrapeResults{
		Found:          true,
		Mods:           mods,
		CurrentPage:    pageNumber,
		TotalPages:     totalPages,
		TotalMods:      totalMods,
		ShownMods:      shownMods,
		ModsIndexStart: modsIndexStart,
		ModsIndexEnd:   modsIndexEnd,
	}, nil
}

// Scrape single mod with all details with a given mod id
func GetMod(modURL string) *models.Mod {
	mod, _ := GetModContext(context.Background(), modURL)
	if mod == nil {
		return &models.Mod{}
	}
	return mod
}

func scenarioField(raw string, label string, nextLabel string) string {
	_, value, found := strings.Cut(raw, label)
	if !found {
		return ""
	}

	if nextLabel != "" {
		value, _, _ = strings.Cut(value, nextLabel)
	}

	return strings.TrimSpace(value)
}

func GetModContext(ctx context.Context, modURL string) (*models.Mod, error) {
	if err := acquireScraper(ctx); err != nil {
		return nil, err
	}
	defer releaseScraper()

	var baseURL string = "reforger.armaplatform.com"
	var mod models.Mod

	mod.Dependencies = []models.Dependency{}
	mod.Scenarios = []models.Scenario{}

	c := newCollector(baseURL)

	// Mod name
	c.OnHTML("section h1", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", e.Text)
		mod.Name = e.Text
	})

	// Mod author
	c.OnHTML("section div.mb-8 span", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", e.Text[3:])
		mod.Author = e.Text[3:]
	})

	// Mod image URL
	c.OnHTML("section figure img[src]", func(e *colly.HTMLElement) {
		// Only get primary cover image
		if e.Attr("alt") == mod.Name {
			mod.ImageURL = resolveWorkshopURL(baseURL, e.Attr("src"))
		}
	})

	// Mod rating
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		if strings.Contains(e.Text, "%") {
			// fmt.Printf("Rating - %s\n", strings.Split(strings.Split(e.Text, "Rating")[1], "Version")[0])
			mod.Rating = strings.Split(strings.Split(e.Text, "Rating")[1], "Version")[0]
		} else {
			mod.Rating = "0%" // Default to no rating
		}
	})

	// Mod version
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("Version - %s\n", strings.Split(strings.Split(e.Text, "Version")[1], "Game")[0])
		mod.Version = strings.Split(strings.Split(e.Text, "Version")[1], "Game")[0]
	})

	// Mod game version
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("Game Version - %s\n", strings.Split(strings.Split(e.Text, "Game Version")[1], "Version size")[0])
		mod.GameVersion = strings.Split(strings.Split(e.Text, "Game Version")[1], "Version size")[0]
	})

	// Mod size
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		if strings.Contains(e.Text, "Subscribers") {
			// fmt.Printf("Size - %s\n", strings.Split(strings.Split(e.Text, "Version size")[1], "Subscribers")[0])
			mod.Size = strings.Split(strings.Split(e.Text, "Version size")[1], "Subscribers")[0]
		} else {
			// fmt.Printf("Size - %s\n", strings.Split(strings.Split(e.Text, "Version size")[1], "Downloads")[0])
			mod.Size = strings.Split(strings.Split(e.Text, "Version size")[1], "Downloads")[0]
		}
	})

	// Mod subscribers
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		if strings.Contains(e.Text, "Subscribers") {
			// fmt.Printf("Subscribers - %s\n", strings.Split(strings.Split(e.Text, "Subscribers")[1], "Downloads")[0])
			mod.Subscribers, _ = strconv.Atoi(strings.Replace(strings.Split(strings.Split(e.Text, "Subscribers")[1], "Downloads")[0], ",", "", -1))
		} else {
			mod.Subscribers = 0
		}
	})

	// Mod downloads
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		if strings.Contains(e.Text, "Downloads") {
			// fmt.Printf("Downloads - %s\n", strings.Split(strings.Split(e.Text, "Downloads")[1], "Created")[0])
			mod.Downloads, _ = strconv.Atoi(strings.Replace(strings.Split(strings.Split(e.Text, "Downloads")[1], "Created")[0], ",", "", -1))
		} else {
			mod.Downloads = 0
		}
	})

	// Mod created
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("Created - %s\n", strings.Split(strings.Split(e.Text, "Created")[1], "Last Modified")[0])
		mod.Created = strings.Split(strings.Split(e.Text, "Created")[1], "Last Modified")[0]
	})

	// Mod last modified
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// In the rare case the mod id begins with id, we don't want to split by string 'id'
		// fmt.Printf("Last Modified - %s\n", strings.Split(e.Text, "Last Modified")[1][0:10])
		mod.LastModified = strings.Split(e.Text, "Last Modified")[1][0:10]
	})

	// Mod ID
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// In the rare case the mod id begins with id, we don't want to split by string 'id'
		// fmt.Printf("ID - %s\n", strings.Split(e.Text, "Last Modified")[1][12:])
		mod.ID = strings.Split(e.Text, "Last Modified")[1][12:]
	})

	// Mod summary
	c.OnHTML("section article pre", func(e *colly.HTMLElement) {
		if mod.Summary == "" {
			// fmt.Printf("%s\n", e.Text)
			mod.Summary = e.Text
		}
	})

	// Mod description
	c.OnHTML("section article div pre", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", e.Text)
		mod.Description = e.Text
	})

	// Mod license
	c.OnHTML("section article p", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", e.Text)
		mod.License = e.Text
	})

	// Mod tags
	c.OnHTML("section div.relative a", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", e.Text)
		mod.Tags = append(mod.Tags, e.Text)
	})

	// Mod Dependencies
	c.OnHTML("section div.flex section.py-8 a", func(e *colly.HTMLElement) {
		// fmt.Printf("Dep - %s\n", e.Attr("href"))
		mod.Dependencies = append(mod.Dependencies, models.Dependency{
			Name:           e.Text,
			OriginalModURL: resolveWorkshopURL(baseURL, e.Attr("href")),
			APIModURL:      fmt.Sprintf("%s/v1/mod/%s", config.GetFullURL(), strings.Split(strings.Split(e.Attr("href"), "/")[2], "-")[0]),
		})
	})

	// Mod Scenarios
	c.OnHTML("section nav.mb-4", func(e *colly.HTMLElement) {
		if !strings.Contains(e.Text, "Scenarios") {
			return
		}

		c1 := newCollector(baseURL)

		c1.OnHTML("section div.grid article", func(e1 *colly.HTMLElement) {
			scenario := models.Scenario{
				Name:        strings.TrimSpace(e1.DOM.Find("h2").First().Text()),
				Description: strings.TrimSpace(e1.DOM.Find("p").First().Text()),
			}

			if imageURL, exists := e1.DOM.Find("img[src]").First().Attr("src"); exists {
				imageURL = strings.TrimSpace(imageURL)
				if imageURL != "" {
					scenario.ImageURL = resolveWorkshopURL(baseURL, imageURL)
				}
			}

			details := strings.Join(
				strings.Fields(e1.DOM.Find("dl").First().Text()),
				" ",
			)

			scenario.ScenarioID = scenarioField(
				details,
				"Scenario ID",
				"Game mode",
			)
			scenario.Gamemode = scenarioField(
				details,
				"Game mode",
				"Player count",
			)

			if rawCount := scenarioField(details, "Player count", ""); rawCount != "" {
				if count, err := strconv.Atoi(rawCount); err == nil {
					scenario.PlayerCount = count
				}
			}

			// Skip empty cards or changed upstream markup we cannot identify.
			if scenario.Name == "" && scenario.ScenarioID == "" {
				return
			}

			mod.Scenarios = append(mod.Scenarios, scenario)
		})

		if err := c1.Visit(fmt.Sprintf("%s/scenarios", modURL)); err != nil {
			zap.S().Warnw("failed to scrape scenarios", "error", err)
		}
	})

	if err := c.Visit(modURL); err != nil {
		return nil, err
	}

	// If no image was found use placeholder
	if mod.ImageURL == "" {
		mod.ImageURL = "https://via.placeholder.com/1280x720"
	}

	mod.OriginalModURL = modURL
	mod.APIModURL = fmt.Sprintf("%s/v1/mod/%s", config.GetFullURL(), mod.ID)

	return &mod, nil
}

func newCollector(baseURL string) *colly.Collector {
	cfg := scraperConfig()
	c := colly.NewCollector(
		colly.AllowedDomains(baseURL),
		colly.UserAgent(cfg.UserAgent),
	)
	c.SetRequestTimeout(cfg.Timeout)
	c.OnError(func(r *colly.Response, err error) {
		status := 0
		if r != nil {
			status = r.StatusCode
		}
		attempt := 0
		if r != nil && r.Request != nil {
			if rawAttempt := r.Request.Ctx.GetAny("attempt"); rawAttempt != nil {
				attempt, _ = rawAttempt.(int)
			}
		}
		if r == nil || r.Request == nil || attempt >= cfg.Retries || !retryable(status, err) {
			zap.S().Warnw("upstream scrape failed", "status", status, "error", err)
			return
		}
		delay := time.Duration(100*(1<<attempt))*time.Millisecond + time.Duration(rand.Intn(150))*time.Millisecond
		time.Sleep(delay)
		r.Request.Ctx.Put("attempt", attempt+1)
		_ = r.Request.Retry()
	})
	c.OnRequest(func(r *colly.Request) {
		if r.Ctx.GetAny("attempt") == nil {
			r.Ctx.Put("attempt", 0)
		}
		zap.S().Debugw("upstream fetch", "url", r.URL.String())
	})
	return c
}

func resolveWorkshopURL(baseURL string, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	base, err := url.Parse("https://" + strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return raw
	}

	ref, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	return base.ResolveReference(ref).String()
}

func acquireScraper(ctx context.Context) error {
	scraper.mu.RLock()
	sem := scraper.sem
	scraper.mu.RUnlock()
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseScraper() {
	scraper.mu.RLock()
	sem := scraper.sem
	scraper.mu.RUnlock()
	<-sem
}

func scraperConfig() ScraperConfig {
	scraper.mu.RLock()
	defer scraper.mu.RUnlock()
	return scraper.cfg
}

func retryable(status int, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout {
		return true
	}
	return status >= 500
}

// absoluteUpstreamURL resolves an image/link reference from a Workshop page.
// The Workshop mixes relative paths and absolute CDN URLs, so the upstream
// host is only prepended when the reference is not already absolute.
func absoluteUpstreamURL(baseURL string, ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if strings.HasPrefix(ref, "//") {
		return "https:" + ref
	}
	return fmt.Sprintf("https://%s%s", baseURL, ref)
}
