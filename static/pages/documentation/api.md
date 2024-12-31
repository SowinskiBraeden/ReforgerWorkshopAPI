# API Documentation
<sup>*Last Updated: 2024-12-30*</sup>

Here you will find all API endpoints and the data they return.

<br><br>

### **Get Mod Preview List Endpoint**

You can query a list of [Mod Preview objects](?page=documentation/mods) from any of the given pages.
This list will *only* return a maximum of `16 mods` at a time.

### **Endpoint**
Get first page.
```
GET /mods
```

Get a specified page.
```
GET /mods/{page_number}
```

### **Curl Example**
```
curl -n -X GET https://api.reforgermods.net/mods/2
```

### **Returned JSON**

Querying this endpoint will return some addition meta data in the json.
```json
{
  "status": "success",
  "meta": {
    "totalPages":     314,
    "currentPage":    2,
    "totalMods":      5024,
    "shownMods":      16,
    "modsIndexStart": 17,
    "modsIndexEnd":   32
  },
  "data":  [{
    "name":          "Super Awesome Mod",
    "author":        "Homer Simpson",
    "imageURL":      "https://example.com/image.png",
    "originalModURL":"https://reforger.armaplatform.com/workshop/{mod_id}",
    "APIModURL":     "https://api.reforgermods.net/mod/{mod_id}",
    "size":          "192.42 KB",
    "rating":        "92%",
    "ID":            "{mod_id}"
  }],
  "links": {
    "next": "https://api.reforgermods.net/mods/3",
    "prev": "https://api.reforgermods.net/mods/1"
  }
}
```

***JSON Meta***

`totalPages` *int*\
The total number of pages that are abled to be queried using `/mods/{page}`.

`currentPage` *int*\
The current page that you've queried.

`totalMods` *int*\
The total mods found from `reforger.armaplatform.com/workshop`.

`shownMods` *int*\
The number of mods returned in the `data` array.

`modsIndexStart` *int*\
The index of the first mod shown in `shownMods`.\
*e.g. "Showing mods `17` to `32`"*  - Where `17` is the index of the first mod in `shownMods` out of the total number of mods in `totalMods`.

`modsIndexEnd` *int*\
The index of the last mod shown in `shownMods`.\
*e.g. "Showing mods `17` to `32`"*  - Where `32` is the index of the last mod in `shownMods` out of the total number of mods in `totalMods`.

***JSON Links***

Depending on the page number that you've requested, the API will return links to the next and previous page of mods for the API.
Allowing easy requests to navigate the pages of mods provided.
___
<br><br>

### **Get Mod Endpoint**

You can query a specific [Mod object](?page=documentation/mods) with the given mod id.
This will return all information for a mod as seen in the [Mod objects definition](?page=documentation/mods).

### **Endpoint**
Get mod.
```
GET /mod/{mod_id}
```

### **Curl Example**
```
curl -n -X GET https://api.reforgermods.net/mod/12345
```

### **Returned JSON**

Querying this endpoint will only return one mod and all of its given information.
```json
{
  "status": "success",
  "mod": {
    "name":           "Super Awesome Mod",
    "author":         "Homer Simpson",
    "originalModURL": "https://reforger.armaplatform.com/workshop/12345",
    "apiModURL":      "https://api.reforgermods.net/mod/12345",
    "imageURL":       "https://example.com/image.png",
    "rating":         "92%",
    "version":        "1.1.0",
    "gameVersion":    "1.1.0.34",
    "size":           "192.42 KB",
    "subscribers":    66677,
    "downloads":      791142,
    "created":        "19.05.2022",
    "lastModified":   "17.03.2024",
    "id":             "12345",
    "summary":        "This is a super awesome mod",
    "description":    "I, Home Simpson made a super awesome mod that adds so much cool stuff to arma reforger!",
    "license":        "Arma Public License (APL)",
    "tags": [ "SUPER", "AWESOME", "MOD", "SIMPSON" ]
  }
}
```
