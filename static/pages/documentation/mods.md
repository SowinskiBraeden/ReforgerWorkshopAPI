# Mod Response Structures
<sup>*Last Updated: 2025-01-05*</sup>

Here you will find the data that is included with mods when querying the [API](?page=documentation/api).

### **Mod Preview Object Structure**

When querying mods by page number to get a list of mods, it only returns a list of previews. These previes do not include
all information regarding the mods.

```json
{
  "name":          "Super Awesome Mod",                                   // string
  "author":        "Homer Simpson",                                       // string
  "imageURL":      "https://example.com/image.png",                       // string
  "originalModURL":"https://reforger.armaplatform.com/workshop/{mod_id}", // string
  "APIModURL":     "https://api.reforgermods.net/mod/{mod_id}",           // string
  "size":          "192.42 KB",                                           // string
  "rating":        "92%",                                                 // string
  "ID":            "{mod_id}"                                             // string
}
```
___
<br><br>

### **Mod Object Structure**

When querying a single mod using the mod id, it will return the full mod object.

```json
{
  "name":           "Super Awesome Mod",                                     // string 
  "author":         "Homer Simpson",                                         // string
  "originalModURL": "https://reforger.armaplatform.com/workshop/12345",      // string
  "apiModURL":      "https://api.reforgermods.net/mod/12345",                // string
  "imageURL":       "https://example.com/image.png",                         // string
  "rating":         "92%",                                                   // string
  "version":        "1.1.0",                                                 // string
  "gameVersion":    "1.1.0.34",                                              // string
  "size":           "192.42 KB",                                             // string
  "subscribers":    66677,                                                   // int
  "downloads":      791142,                                                  // int
  "created":        "19.05.2022",                                            // string
  "lastModified":   "17.03.2024",                                            // string
  "id":             "12345",                                                 // string
  "summary":        "This is a super awesome mod",                           // string
  "description":    "Big awesome mod!",                                      // string
  "license":        "Arma Public License (APL)",                             // string
  "tags": [ "SUPER", "AWESOME", "MOD", "SIMPSON" ],                          // string array
  "dependencies": [{                                                         // object array
    "name":           "Mod Dependency 1",                                    // string
    "originalModURL": "https://reforger.armaplatform.com/workshop/{dep_id}", // string
    "apiModURL":      "https://api.reforgermods.net/mod/{dep_id}"            // string
  }],
  "scenarios": [{                                                            // object array
    "name":        "Some Scenario",                                          // string
    "description": "You will do awesome things in this scenario",            // string
    "scenarioID":  "{12345}Missions/SomeScenario.conf",                      // string
    "gamemode":    "Campaign",                                               // string
    "playerCount": 64,                                                       // int
    "imageURL":    "https://example.com/image.png",                          // string
  }]
}
```
___
<br><br>

### **Dependency object structure**

Some mods have dependencies, when querying a mod with one or more dependencies you will find an array of these dependency objects.
```json
{
  "name":           "Mod Dependency",                                      // string
  "originalModURL": "https://reforger.armaplatform.com/workshop/{dep_id}", // string
  "apiModURL":      "https://api.reforgermods.net/mod/{dep_id}"            // string
}
```
___
<br><br>

### **Scenario object structure**

Some mods come with new scenarios to play, when querying a mod with one or more scenarios you will find an array of these scenario objects.
```json
{
  "name":        "Some Scenario",                               // string
  "description": "You will do awesome things in this scenario", // string
  "scenarioID":  "{12345}Missions/SomeScenario.conf",           // string
  "gamemode":    "Campaign",                                    // string
  "playerCount": 64,                                            // int
  "imageURL":    "https://example.com/image.png",               // string
}
```
___
<br><br>
