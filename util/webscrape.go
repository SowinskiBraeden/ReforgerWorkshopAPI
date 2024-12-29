package util

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"

	"github.com/gocolly/colly"
)

const RESULTS_PER_PAGE = 16

func ScrapeMods(pageNumber int) models.WebScrapeResults {
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

	// Page information
	var totalPages int
	var resultSummary string

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
		// fmt.Printf("Image URL -> %s\n", fmt.Sprintf("%s&w=3840&q=75", strings.Split(strings.Split(e.Text, "srcSet=\"")[1], "&")[0]))
		var url string
		if strings.Contains(e.Text, "srcSet=\"") {
			url = fmt.Sprintf("https://%s%s&w=3840&q=75", baseURL, strings.Split(strings.Split(e.Text, "srcSet=\"")[1], "&")[0])
		} else {
			// For some reason image src does not exists, use this placeholder as
			// used on reforger.armaplatform.com/workshop for mods with no image
			url = "https://via.placeholder.com/1280x720"
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
		return models.WebScrapeResults{
			Found: false,
		}
	}

	// Create mod structs from results
	for i := 0; i < len(names); i++ {
		mods = append(mods, models.ModPreview{
			Name:     names[i],
			Author:   authors[i],
			ImageURL: imageURLs[i],
			ModURL:   modURLs[i],
			Size:     sizes[i],
			Rating:   ratings[i],
		})
	}

	return models.WebScrapeResults{
		Found:       true,
		Mods:        mods,
		CurrentPage: pageNumber,
		TotalPages:  totalPages,
	}
}

func GetMod(modURL string) models.Mod {
	var baseURL string = "reforger.armaplatform.com"
	var mod models.Mod

	c := colly.NewCollector(
		colly.AllowedDomains(baseURL),
	)

	// Check if mod is found
	c.OnHTML("section h1", func(e *colly.HTMLElement) {
		fmt.Printf("%s\n", e.Text)
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
		// fmt.Printf("%s\n", strings.Split(strings.Split(e.Text, "Rating")[1], "Version")[0])
		mod.Rating = strings.Split(strings.Split(e.Text, "Rating")[1], "Version")[0]
	})

	// Mod version
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", strings.Split(strings.Split(e.Text, "Version")[1], "Game")[0])
		mod.Version = strings.Split(strings.Split(e.Text, "Version")[1], "Game")[0]
	})

	// Mod game version
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", strings.Split(strings.Split(e.Text, "Game Version")[1], "Version size")[0])
		mod.GameVersion = strings.Split(strings.Split(e.Text, "Game Version")[1], "Version size")[0]
	})

	// Mod size
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", strings.Split(strings.Split(e.Text, "Version size")[1], "Subscribers")[0])
		mod.Rating = strings.Split(strings.Split(e.Text, "Version size")[1], "Subscribers")[0]
	})

	// Mod subscribers
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", strings.Split(strings.Split(e.Text, "Subscribers")[1], "Downloads")[0])
		mod.Subscribers = strings.Split(strings.Split(e.Text, "Subscribers")[1], "Downloads")[0]
	})

	// Mod downloads
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", strings.Split(strings.Split(e.Text, "Downloads")[1], "Created")[0])
		mod.Downloads = strings.Split(strings.Split(e.Text, "Downloads")[1], "Created")[0]
	})

	// Mod created
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// fmt.Printf("%s\n", strings.Split(strings.Split(e.Text, "Created")[1], "Last Modified")[0])
		mod.Created = strings.Split(strings.Split(e.Text, "Created")[1], "Last Modified")[0]
	})

	// Mod last modified
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// In the rare case the mod id begins with id, we don't want to split by string 'id'
		// fmt.Printf("%s\n", strings.Split(e.Text, "Last Modified")[1][0:10])
		mod.LastModified = strings.Split(e.Text, "Last Modified")[1][0:10]
	})

	// Mod ID
	c.OnHTML("section dl", func(e *colly.HTMLElement) {
		// In the rare case the mod id begins with id, we don't want to split by string 'id'
		// fmt.Printf("%s\n", strings.Split(e.Text, "Last Modified")[1][12:])
		mod.ID = strings.Split(e.Text, "Last Modified")[1][12:]
	})

	// Mod summary + description
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

	mod.ModURL = modURL

	c.Visit(modURL)

	return mod
}
