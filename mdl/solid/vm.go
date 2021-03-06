// Copyright 2016 The Gofem Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package solid

import (
	"github.com/cpmech/gosl/chk"
	"github.com/cpmech/gosl/fun"
	"github.com/cpmech/gosl/tsr"
)

// VonMises implements von Mises plasticity model
type VonMises struct {
	SmallElasticity
	qy0 float64   // initial qy
	H   float64   // hardening variable
	rho float64   // density
	ten []float64 // auxiliary tensor
}

// add model to factory
func init() {
	allocators["vm"] = func() Model { return new(VonMises) }
}

// Clean clean resources
func (o *VonMises) Clean() {
}

// GetRho returns density
func (o *VonMises) GetRho() float64 {
	return o.rho
}

// Init initialises model
func (o *VonMises) Init(ndim int, pstress bool, prms fun.Prms) (err error) {

	// parse parameters
	err = o.SmallElasticity.Init(ndim, pstress, prms)
	if err != nil {
		return
	}
	for _, p := range prms {
		switch p.N {
		case "qy0":
			o.qy0 = p.V
		case "H":
			o.H = p.V
		case "rho":
			o.rho = p.V
		case "E", "nu", "l", "G", "K":
		default:
			return chk.Err("vm: parameter named %q is incorrect\n", p.N)
		}
	}

	// auxiliary structures
	o.ten = make([]float64, o.Nsig)
	return
}

// GetPrms gets (an example) of parameters
func (o VonMises) GetPrms() fun.Prms {
	return []*fun.Prm{
		&fun.Prm{N: "qy0", V: 0.5},
		&fun.Prm{N: "H", V: 0},
	}
}

// InitIntVars initialises internal (secondary) variables
func (o VonMises) InitIntVars(σ []float64) (s *State, err error) {
	s = NewState(o.Nsig, 1, false, false)
	copy(s.Sig, σ)
	return
}

// Update updates stresses for given strains
func (o *VonMises) Update(s *State, ε, Δε []float64, eid, ipid int, time float64) (err error) {

	// set flags
	s.Loading = false    // => not elastoplastic
	s.ApexReturn = false // => not return-to-apex
	s.Dgam = 0           // Δγ := 0

	// accessors
	σ := s.Sig
	α0 := &s.Alp[0]

	// trial stress
	var devΔε_i float64
	trΔε := Δε[0] + Δε[1] + Δε[2]
	for i := 0; i < o.Nsig; i++ {
		devΔε_i = Δε[i] - trΔε*tsr.Im[i]/3.0
		o.ten[i] = σ[i] + o.K*trΔε*tsr.Im[i] + 2.0*o.G*devΔε_i // ten := σtr
	}
	ptr, qtr := tsr.M_p(o.ten), tsr.M_q(o.ten)

	// trial yield function
	ftr := qtr - o.qy0 - o.H*(*α0)

	// elastic update
	if ftr <= 0.0 {
		copy(σ, o.ten) // σ := ten = σtr
		return
	}

	// elastoplastic update
	var str_i float64
	hp := 3.0*o.G + o.H
	s.Dgam = ftr / hp
	*α0 += s.Dgam
	pnew := ptr
	m := 1.0 - s.Dgam*3.0*o.G/qtr
	for i := 0; i < o.Nsig; i++ {
		str_i = o.ten[i] + ptr*tsr.Im[i]
		σ[i] = m*str_i - pnew*tsr.Im[i]
	}
	s.Loading = true
	return
}

// CalcD computes D = dσ_new/dε_new consistent with StressUpdate
func (o *VonMises) CalcD(D [][]float64, s *State, firstIt bool) (err error) {

	// set first Δγ
	if firstIt {
		s.Dgam = 0
	}

	// elastic
	if !s.Loading {
		return o.SmallElasticity.CalcD(D, s)
	}

	// elastoplastic => consistent stiffness
	σ := s.Sig
	Δγ := s.Dgam
	p, q := tsr.M_p(σ), tsr.M_q(σ)
	qtr := q + Δγ*3.0*o.G
	m := 1.0 - Δγ*3.0*o.G/qtr
	nstr := tsr.SQ2by3 * qtr // norm(str)
	for i := 0; i < o.Nsig; i++ {
		o.ten[i] = (σ[i] + p*tsr.Im[i]) / (m * nstr) // ten := unit(str) = snew / (m * nstr)
	}
	hp := 3.0*o.G + o.H
	a1 := o.K
	b2 := 6.0 * o.G * o.G * (Δγ/qtr - 1.0/hp)
	for i := 0; i < o.Nsig; i++ {
		for j := 0; j < o.Nsig; j++ {
			D[i][j] = 2.0*o.G*m*tsr.Psd[i][j] + a1*tsr.Im[i]*tsr.Im[j] + b2*o.ten[i]*o.ten[j]
		}
	}
	return
}

// ContD computes D = dσ_new/dε_new continuous
func (o *VonMises) ContD(D [][]float64, s *State) (err error) {

	// elastic part
	err = o.SmallElasticity.CalcD(D, s)
	if err != nil {
		return
	}

	// only elastic
	if !s.Loading {
		return
	}

	// elastoplastic
	σ := s.Sig
	d1 := 3.0*o.G + o.H
	a4 := 6.0 * o.G * o.G / d1
	sno, _, _ := tsr.M_devσ(o.ten, σ) // ten := dev(σ)
	for i := 0; i < o.Nsig; i++ {
		for j := 0; j < o.Nsig; j++ {
			D[i][j] -= a4 * o.ten[i] * o.ten[j] / (sno * sno)
		}
	}
	return
}

// EPmodel ///////////////////////////////////////////////////////////////////////////////////////////

// Info returns some information and data from this model
func (o VonMises) Info() (nalp, nsurf int) {
	return 1, 1
}

// Get_phi gets φ or returns 0
func (o VonMises) Get_phi() float64 { return 0 }

// Get_bsmp gets b coefficient if using SMP invariants
func (o VonMises) Get_bsmp() float64 { return 0 }

// Set_bsmp sets b coefficient if using SMP invariants
func (o *VonMises) Set_bsmp(b float64) {}

// L_YieldFunc computes the yield function value for given principal stresses (σ)
func (o *VonMises) L_YieldFunc(σ, α []float64) float64 {
	chk.Panic("VonMises: L_YieldFunc is not implemented yet")
	return 0
}

// YieldFs computes the yield functions
func (o VonMises) YieldFuncs(s *State) []float64 {
	q := tsr.M_q(s.Sig)
	α0 := s.Alp[0]
	return []float64{q - o.qy0 - o.H*α0}
}

// ElastUpdate updates state with an elastic response
func (o VonMises) ElastUpdate(s *State, ε []float64) {
	var devε_i float64
	trε := ε[0] + ε[1] + ε[2]
	for i := 0; i < o.Nsig; i++ {
		devε_i = ε[i] - trε*tsr.Im[i]/3.0
		s.Sig[i] = o.K*trε*tsr.Im[i] + 2.0*o.G*devε_i
	}
}

// ElastD returns continuum elastic D
func (o VonMises) ElastD(D [][]float64, s *State) {
}

// E_CalcSig computes principal stresses for given principal elastic strains
func (o VonMises) E_CalcSig(σ, εe []float64) {
}

// E_CalcDe computes elastic modulus in principal components
func (o VonMises) E_CalcDe(De [][]float64, εe []float64) {
}

// L_FlowHard computes model variabes for given principal values
func (o VonMises) L_FlowHard(Nb, h, σ, α []float64) (f float64, err error) {
	return
}

// L_SecondDerivs computes second order derivatives
//  N    -- ∂f/∂σ     [nsig]
//  Nb   -- ∂g/∂σ     [nsig]
//  A    -- ∂f/∂α_i   [nalp]
//  h    -- hardening [nalp]
//  Mb   -- ∂Nb/∂εe   [nsig][nsig]
//  a_i  -- ∂Nb/∂α_i  [nalp][nsig]
//  b_i  -- ∂h_i/∂εe  [nalp][nsig]
//  c_ij -- ∂h_i/∂α_j [nalp][nalp]
func (o VonMises) L_SecondDerivs(N, Nb, A, h []float64, Mb, a, b, c [][]float64, σ, α []float64) (err error) {
	return
}
