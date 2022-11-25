# risefront

`code.pfad.fr/risefront` is a package for gracefully upgrading the server behing a tcp connection with zero-downtime (without disturbing running transfers or dropping incoming requests).

Inspired by [overseer](https://github.com/jpillora/overseer), but re-engineered to also work on windows!

![diagram](./overseer.png)

## [Documentation and examples on code.pfad.fr/risefront](https://code.pfad.fr/risefront)

The name is meant to be the opposite of fallback (since it switches to a new executable when asked to do so).

## Underlying logic

When you start multiple instances of a program wrapped in `risefront`:

- the first one (the parent `o`) will actually listen to the addresses and call `Run`
- when a second instance (child `A`) is started, it detects that a parent is already running
  - it creates some random sockets to locally listen to
  - it informs the parent, that it wants to take over
  - it calls `Run` with those "proxy listeners"
  - from now on, the parent will forward all new connections to those "proxy listeners"
- if a third instance is started (child `B`), the same steps happen and the second instance (child `A`) will exit after handling its last request
