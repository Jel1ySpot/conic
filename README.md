> This project is in early developing. We need your help to make it perfect
# conic
___
> Another easy way for configuration

## Install
```shell
go get -u github.com/Jel1ySpot/conic
```

## How conic works?
By binding variable and configuration, you can access and change the configuration in a easy way.
> Configuration (usually a file) <=> Go Variable

## Getting start
### Quick start
```go
package main

import (
    "fmt"
    "github.com/Jel1ySpot/conic"
)

type Config struct {
    Name string `json:"name"`
    Age  int    `json:"age"`
}

func main() {
    config := Config{}
    
    // Binding
    conic.BindRef("", &config)

    // Read configuration file
    conic.SetConfigFile("config.json")
    conic.ReadInConfig()
    conic.WatchConfig()
    /* config.json
       {
         "name": "alex",
         "age": 21
       }
    */

    fmt.Printf("&v", config)
    
    // Change value
    config.Name = "Richard"
    config.Age += 7
    conic.WriteConfig()
    /* config.json
    {
      "name": "alex",
      "age": 28
    }
    */
}
```
