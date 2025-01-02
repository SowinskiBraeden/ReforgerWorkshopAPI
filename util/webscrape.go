package util

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"

	"github.com/gocolly/colly"
)

const RESULTS_PER_PAGE = 16

// scrapes multiple mods from a given workshop page
func ScrapeMods(pageNumber int) (*models.WebScrapeResults, error) {
	var baseURL string = "reforger.armaplatform.com"
	workshopURL := fmt.Sprintf("https://%s/workshop?page=%d", baseURL, pageNumber)
	var mods []models.ModPreview

	c := colly.NewCollector(
		colly.AllowedDomains(baseURL),
	)

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
		var url string
		if strings.Contains(e.Text, "srcSet=\"") {
			url = fmt.Sprintf("https://%s%s&w=1080&q=100", baseURL, strings.Split(strings.Split(e.Text, "srcSet=\"")[1], "&")[0])
		} else {
			// For some reason image src does not exists, use this placeholder as
			// used on reforger.armaplatform.com/workshop for mods with no image
			// url = "https://via.placeholder.com/1280x720"
			url = "https://via.placeholder.com/640x360"
		}
		imageURLs = append(imageURLs, url)
	})

	// Mod URLs
	c.OnHTML("div.grid a.group[href]", func(e *colly.HTMLElement) {
		modURLs = append(modURLs, fmt.Sprintf("https://%s%s", baseURL, e.Attr("href")))
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

	c.Visit(workshopURL)

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
			APIModURL:      fmt.Sprintf("%s/mod/%s", config.GetFullURL(), modID),
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
	var baseURL string = "reforger.armaplatform.com"
	var mod models.Mod

	mod.Dependencies = []models.Dependency{}

	c := colly.NewCollector(
		colly.AllowedDomains(baseURL),
	)

	// Check if mod is found
	c.OnHTML("section h1", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", e.Text)
	})

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
			// fmt.Printf("https://%s%s\n", baseURL, e.Attr("src"))
			mod.ImageURL = fmt.Sprintf("https://%s%s", baseURL, e.Attr("src"))
		}
	})

	// Mod rating
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("Rating - %s\n", strings.Split(strings.Split(e.Text, "Rating")[1], "Version")[0])
		mod.Rating = strings.Split(strings.Split(e.Text, "Rating")[1], "Version")[0]
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
			var err error
			mod.Subscribers, err = strconv.Atoi(strings.Replace(strings.Split(strings.Split(e.Text, "Subscribers")[1], "Downloads")[0], ",", "", -1))
			if err != nil {
				return
			}
		} else {
			mod.Subscribers = 0
		}
	})

	// Mod downloads
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("Downloads - %s\n", strings.Split(strings.Split(e.Text, "Downloads")[1], "Created")[0])
		var err error
		mod.Downloads, err = strconv.Atoi(strings.Replace(strings.Split(strings.Split(e.Text, "Downloads")[1], "Created")[0], ",", "", -1))
		if err != nil {
			return
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
			OriginalModURL: fmt.Sprintf("https://%s%s", baseURL, e.Attr("href")),
			APIModURL:      fmt.Sprintf("%s/mod/%s", config.GetFullURL(), strings.Split(strings.Split(e.Attr("href"), "/")[2], "-")[0]),
		})
	})

	c.Visit(modURL)

	// If no image was found use placeholder
	if mod.ImageURL == "" {
		mod.ImageURL = "https://via.placeholder.com/1280x720"
	}

	mod.OriginalModURL = modURL
	mod.APIModURL = fmt.Sprintf("%s/mod/%s", config.GetFullURL(), mod.ID)

	return &mod
}
