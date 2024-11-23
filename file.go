package conic

import "os"

func WriteConfig() error { return c.WriteConfig() }

func (c *Conic) WriteConfig() error {
    if err := c.marshalAll(); err != nil {
        return err
    }

    b, err := c.adapter.Encode(c.config)
    if err != nil {
        return err
    }

    if err := os.WriteFile(c.configFile, b, os.ModePerm); err != nil {
        return err
    }

    return nil
}
