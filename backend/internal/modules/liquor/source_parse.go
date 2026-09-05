package liquor

import "fmt"

type rawList struct {
	Count int          `json:"count"`
	Items []rawProduct `json:"list"`
}

type rawProduct struct {
	ID             int64  `json:"liquor_id"`
	Name           string `json:"name"`
	Specifications string `json:"specifications"`
	Unit           string `json:"unit"`
	Sort           int    `json:"sort"`
	Price          *int64 `json:"price"`
	Change         *int64 `json:"price_change"`
	Date           string `json:"price_date"`
}

type rawPoint struct {
	Date   string `json:"date"`
	Price  *int64 `json:"price"`
	Change *int64 `json:"price_change"`
	Unit   string `json:"unit"`
}

type rawDetail struct {
	Product rawProduct `json:"detail"`
	History []rawPoint `json:"history"`
}

func (p rawPoint) parse() (Point, error) {
	if !validDate(p.Date) || p.Unit != Unit || p.Price == nil || p.Change == nil {
		return Point{}, fmt.Errorf("%w: quote fields", ErrSourceData)
	}
	const maxYuan = 1_000_000_000
	if *p.Price <= 0 || *p.Price > maxYuan || *p.Change < -maxYuan || *p.Change > maxYuan {
		return Point{}, fmt.Errorf("%w: quote amount", ErrSourceData)
	}
	return Point{Date: p.Date, Price: Cents(*p.Price * 100), Change: Cents(*p.Change * 100)}, nil
}

func (p rawProduct) parse() (Product, Point, error) {
	if p.ID <= 0 || p.Name == "" || len(p.Name) > 512 || p.Specifications == "" || len(p.Specifications) > 512 {
		return Product{}, Point{}, fmt.Errorf("%w: product fields", ErrSourceData)
	}
	point, err := (rawPoint{Date: p.Date, Price: p.Price, Change: p.Change, Unit: p.Unit}).parse()
	if err != nil {
		return Product{}, Point{}, err
	}
	return Product{ID: ProductID(p.ID), Name: p.Name, Specifications: p.Specifications, Unit: p.Unit, Sort: p.Sort}, point, nil
}

func (d rawDetail) parse(product Product, latest Point) (Series, error) {
	actual, current, err := d.Product.parse()
	if err != nil {
		return Series{}, err
	}
	if actual != product || current != latest || len(d.History) == 0 || len(d.History) > 366 {
		return Series{}, fmt.Errorf("%w: list and detail disagree", ErrSourceData)
	}
	series := Series{Product: product, Prices: make([]Point, 0, len(d.History))}
	seen := make(map[string]bool)
	foundLatest := false
	for _, item := range d.History {
		point, err := item.parse()
		if err != nil {
			return Series{}, err
		}
		if seen[point.Date] || point.Date > latest.Date {
			return Series{}, fmt.Errorf("%w: duplicate or future history", ErrSourceData)
		}
		seen[point.Date] = true
		if point.Date == latest.Date {
			if point != latest {
				return Series{}, fmt.Errorf("%w: history price mismatch", ErrSourceData)
			}
			foundLatest = true
		}
		series.Prices = append(series.Prices, point)
	}
	if !foundLatest {
		return Series{}, fmt.Errorf("%w: latest quote missing from history", ErrSourceData)
	}
	return series, nil
}
