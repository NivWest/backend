package simmarket

// instrument is one fictional company on the simulated market. The price path parameters (s0, mu,
// sigma, seed) are fixed so every process produces the same prices; the seeds were chosen so every
// path stays within [10, 5000] SEK for at least three years after Epoch (see TestPriceBounds).
type instrument struct {
	symbol      string
	name        string
	sector      string
	description string

	s0    float64 // start price at Epoch, SEK
	mu    float64 // annual drift
	sigma float64 // annual volatility
	seed  uint64  // per-instrument seed of the random walk

	volScale int64 // per-minute volume multiplier

	shares float64 // shares outstanding
	eps    float64 // earnings per share, SEK (<= 0: loss-making, no P/E)
	dps    float64 // annual dividend per share, SEK
}

func (in *instrument) orderbookID() string { return idPrefix + in.symbol }

const (
	idPrefix = "SIM-"
	currency = "SEK"
	market   = "Simulated"
)

// universe is the fixed instrument list, in display order. Later stories' tests depend on these ids and names.
var universe = []instrument{
	{
		symbol: "FJRD", name: "Fjord Robotics", sector: "Industrials",
		description: "Fjord Robotics builds autonomous handling robots for ports, warehouses and offshore platforms. " +
			"Its subscription-based fleet software is the fastest-growing part of the business.",
		s0: 245, mu: 0.12, sigma: 0.32, seed: 0x5EED0001, volScale: 4,
		shares: 180e6, eps: 6.80, dps: 1.20,
	},
	{
		symbol: "NVE", name: "Nordvik Energy", sector: "Energy",
		description: "Nordvik Energy operates hydropower stations and a regional electricity grid in northern Scandinavia. " +
			"Stable cash flows fund a progressive dividend policy.",
		s0: 132, mu: 0.04, sigma: 0.22, seed: 0x5EED0002, volScale: 5,
		shares: 950e6, eps: 9.40, dps: 4.80,
	},
	{
		symbol: "KALL", name: "Kallsten Bank", sector: "Financials",
		description: "Kallsten Bank is a full-service retail and corporate bank with branches across the Nordics. " +
			"Mortgage lending makes up most of its loan book, and it pays out about half its earnings.",
		s0: 118, mu: 0.05, sigma: 0.18, seed: 0x5EED0003, volScale: 6,
		shares: 1.6e9, eps: 12.10, dps: 6.20,
	},
	{
		symbol: "SOLV", name: "Solvik Pharma", sector: "Health Care",
		description: "Solvik Pharma develops antibody treatments for autoimmune diseases. " +
			"Its lead candidate is in phase III trials, and the company is not yet profitable.",
		s0: 86, mu: 0.10, sigma: 0.45, seed: 0x5EED0004, volScale: 3,
		shares: 240e6, eps: -3.40, dps: 0,
	},
	{
		symbol: "AURG", name: "Aurora Gaming", sector: "Communication Services",
		description: "Aurora Gaming is an independent studio behind a handful of popular online multiplayer titles. " +
			"Revenue swings with each release, and all profits are reinvested in new games.",
		s0: 178, mu: 0.15, sigma: 0.55, seed: 0x5EED0005, volScale: 3,
		shares: 120e6, eps: 2.10, dps: 0,
	},
	{
		symbol: "LIND", name: "Lindqvist Retail", sector: "Consumer Discretionary",
		description: "Lindqvist Retail runs a chain of home furnishing and outdoor stores alongside a growing web shop. " +
			"Margins are thin but the dividend has been paid every year since its listing.",
		s0: 64, mu: 0.02, sigma: 0.26, seed: 0x5EED0006, volScale: 4,
		shares: 410e6, eps: 4.10, dps: 2.20,
	},
	{
		symbol: "POLT", name: "Polar Telecom", sector: "Telecommunications",
		description: "Polar Telecom provides mobile, fibre and TV services to households and businesses in Sweden and Norway. " +
			"A mature, defensive business known for its high dividend yield.",
		s0: 42.5, mu: 0.01, sigma: 0.14, seed: 0x5EED0007, volScale: 6,
		shares: 3.9e9, eps: 3.20, dps: 2.10,
	},
	{
		symbol: "SKOG", name: "Skogsberg Timber", sector: "Materials",
		description: "Skogsberg Timber owns forest land and sawmills producing construction timber and pulp. " +
			"Earnings follow the building cycle and global pulp prices.",
		s0: 156, mu: 0.03, sigma: 0.24, seed: 0x5EED0008, volScale: 3,
		shares: 520e6, eps: 11.30, dps: 4.50,
	},
	{
		symbol: "HAVN", name: "Havnes Shipping", sector: "Industrials",
		description: "Havnes Shipping operates a fleet of dry bulk carriers and car transport vessels. " +
			"Freight rates drive large swings in profits, and surplus cash goes back to shareholders.",
		s0: 212, mu: 0.06, sigma: 0.38, seed: 0x5EED0009, volScale: 2,
		shares: 210e6, eps: 24.50, dps: 9.00,
	},
	{
		symbol: "MALM", name: "Malmgren Mining", sector: "Materials",
		description: "Malmgren Mining extracts copper and zinc from two underground mines in the Bergslagen region. " +
			"The share price is highly sensitive to metal prices and exploration news.",
		s0: 96, mu: 0.07, sigma: 0.55, seed: 0x5EED000A, volScale: 4,
		shares: 330e6, eps: 7.20, dps: 1.50,
	},
	{
		symbol: "VIND", name: "Vindkraft Nord", sector: "Utilities",
		description: "Vindkraft Nord develops and operates onshore wind farms in the north of Sweden. " +
			"It is investing heavily in new capacity and does not pay a dividend yet.",
		s0: 74, mu: 0.13, sigma: 0.42, seed: 0x5EED000B, volScale: 3,
		shares: 280e6, eps: 0.85, dps: 0,
	},
	{
		symbol: "BRYG", name: "Brygga Foods", sector: "Consumer Staples",
		description: "Brygga Foods makes seafood, dairy and ready meals sold under well-known Nordic brands. " +
			"A defensive company with steady volumes and a reliable dividend.",
		s0: 315, mu: 0.04, sigma: 0.12, seed: 0x5EED000C, volScale: 2,
		shares: 150e6, eps: 16.40, dps: 8.20,
	},
}
