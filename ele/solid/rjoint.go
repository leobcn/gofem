// Copyright 2016 The Gofem Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package solid

import (
	"github.com/cpmech/gofem/ele"
	"github.com/cpmech/gofem/inp"
	"github.com/cpmech/gofem/mdl/solid"

	"github.com/cpmech/gosl/chk"
	"github.com/cpmech/gosl/fun"
	"github.com/cpmech/gosl/io"
	"github.com/cpmech/gosl/la"
	"github.com/cpmech/gosl/tsr"
	"github.com/cpmech/gosl/utl"
)

// Rjoint implements the rod-joint (interface/link) element for reinforced solids.
//  The following convention is considered:
//   n or N   -- means [N]odes
//   p or P   -- means integration [P]oints
//   nn or Nn -- number of nodes
//   np or Np -- number of integration [P]points
//   ndim     -- space dimension
//   nsig     -- number of stress/strain components == 2 * ndim
//   rod      -- means rod element
//   rodH     -- rod shape structure
//   rodNn    -- rod number of nodes
//   rodNp    -- rod number of integration points
//   rodS     -- rod shape functions
//   sld      -- means solid element
//   sldH     -- rod shape structure
//   sldNn    -- solid number of nodes
//   sldNp    -- solid number of integration points
//   sldS     -- solid shape functions
//   rodYn    -- rod's (real) coordinates of node
//   rodYp    -- rod's (real) coordinates of integration point
//   r or R   -- means natural coordinates in the solids' system
//   z or Z   -- means natural coordinates in the rod's system
//   s or S   -- parametric coordinate along rod
//   rodRn    -- natural coordinates or rod's nodes w.r.t solid's system
//   rodRp    -- natural coordinates of rod's integration point w.r.t to solid's system
//   Nmat     -- solid shape functions evaluated at rod nodes
//   Pmat     -- solid shape functions evaluated at rod integration points
//  References:
//   [1] Durand R, Farias MM, Pedroso DM. Modelling the strengthening of solids with
//       incompatible line finite elements. Submitted.
//   [2] Durand R, Farias MM, Pedroso DM. Computing intersections between non-compatible
//       curves and finite elements. Computational Mechanics, 56(3):463-475; 2015
//       http://dx.doi.org/10.1007/s00466-015-1181-y
//   [3] Durand R and Farias MM. A local extrapolation method for finite elements.
//       Advances in Engineering Software, 67:1-9; 2014
//       http://dx.doi.org/10.1016/j.advengsoft.2013.07.002
type Rjoint struct {

	// basic data
	Sim  *inp.Simulation // simulation
	Edat *inp.ElemData   // element data; stored in allocator to be used in Connect
	Cell *inp.Cell       // the cell structure
	Ny   int             // total number of dofs == rod.Nu + solid.Nu
	Ndim int             // space dimension

	// essential
	Rod *Rod            // rod element
	Sld *Solid          // solid element
	Mdl *solid.RjointM1 // material model

	// shape functions evaluations and extrapolator matrices
	Nmat [][]float64 // [sldNn][rodNn] shape functions of solids @ [N]odes of rod element
	Pmat [][]float64 // [sldNn][rodNp] shape functions of solids @ integration [P]oints of rod element (for Coulomb model)
	Emat [][]float64 // [sldNn][sldNp] solid's extrapolation matrix (for Coulomb model)

	// variables for Coulomb model
	Coulomb bool        // use Coulomb model
	rodRp   [][]float64 // [rodNp][ndim] natural coordinates of ips of rod w.r.t. solid's system
	σNo     [][]float64 // [nneSld][nsig] σ at nodes of solid
	σIp     []float64   // [nsig] σ at ips of rod
	t1      []float64   // [ndim] traction vectors for σc
	t2      []float64   // [ndim] traction vectors for σc

	// corotational system aligned with rod element
	e0 [][]float64 // [rodNp][ndim] local directions at each integration point of rod
	e1 [][]float64 // [rodNp][ndim] local directions at each integration point of rod
	e2 [][]float64 // [rodNp][ndim] local directions at each integration point of rod

	// auxiliary variables
	ΔuC [][]float64 // [rodNn][ndim] relative displ. increment of solid @ nodes of rod; Eq (30)
	Δw  []float64   // [ndim] relative velocity; Eq (32)
	qb  []float64   // [ndim] resultant traction vector 'holding' the rod @ ip; Eq (34)
	fC  []float64   // [rodNu] internal/contact forces vector; Eq (34)

	// temporary Jacobian matrices. see Eq. (57)
	Krr [][]float64 // [rodNu][rodNu] Eq. (58)
	Krs [][]float64 // [rodNu][sldNu] Eq. (59)
	Ksr [][]float64 // [sldNu][rodNu] Eq. (60)
	Kss [][]float64 // [sldNu][sldNu] Eq. (61)

	// internal values
	States    []*solid.OnedState // [nip] internal states
	StatesBkp []*solid.OnedState // [nip] backup internal states
	StatesAux []*solid.OnedState // [nip] backup internal states

	// extra variables for consistent tangent operator
	Ncns   bool            // use non-consistent model
	T1     [][]float64     // [rodNp][nsig] tensor (e1 dy e1)
	T2     [][]float64     // [rodNp][nsig] tensor (e2 dy e2)
	DσNoDu [][][][]float64 // [sldNn][nsig][sldNn][ndim] ∂σSldNod/∂uSldNod : derivatives of σ @ nodes of solid w.r.t displacements of solid
	DσDun  [][]float64     // [nsig][ndim] ∂σIp/∂us : derivatives of σ @ ip of solid w.r.t displacements of solid
}

// initialisation ///////////////////////////////////////////////////////////////////////////////////

// register element
func init() {

	// information allocator
	ele.SetInfoFunc("rjoint", func(sim *inp.Simulation, cell *inp.Cell, edat *inp.ElemData) *ele.Info {
		return &ele.Info{}
	})

	// element allocator
	ele.SetAllocator("rjoint", func(sim *inp.Simulation, cell *inp.Cell, edat *inp.ElemData, x [][]float64) ele.Element {
		var o Rjoint
		o.Sim = sim
		o.Edat = edat
		o.Cell = cell
		o.Ndim = sim.Ndim
		if s_ncns, found := io.Keycode(edat.Extra, "ncns"); found {
			o.Ncns = io.Atob(s_ncns)
		}
		return &o
	})
}

// Id returns the cell Id
func (o *Rjoint) Id() int { return o.Cell.Id }

// Connect connects rod/solid elements in this Rjoint
func (o *Rjoint) Connect(cid2elem []ele.Element, c *inp.Cell) (nnzK int, err error) {

	// get rod and solid elements
	rodId := c.JlinId
	sldId := c.JsldId
	o.Rod = cid2elem[rodId].(*Rod)
	o.Sld = cid2elem[sldId].(*Solid)
	if o.Rod == nil {
		err = chk.Err("cannot find joint's rod cell with id == %d", rodId)
		return
	}
	if o.Sld == nil {
		err = chk.Err("cannot find joint's solid cell with id == %d", sldId)
		return
	}

	// total number of dofs
	o.Ny = o.Rod.Nu + o.Sld.Nu

	// model
	mat := o.Sim.MatModels.Get(o.Edat.Mat)
	if mat == nil {
		err = chk.Err("materials database failed on getting %q material\n", o.Edat.Mat)
		return
	}
	o.Mdl = mat.Sld.(*solid.RjointM1)

	// flag
	o.Coulomb = o.Mdl.A_μ > 0

	// auxiliary
	nsig := 2 * o.Ndim

	// rod data
	rodH := o.Rod.Cell.Shp
	rodNp := len(o.Rod.IpsElem)
	rodNn := rodH.Nverts
	rodNu := o.Rod.Nu

	// solid data
	sldH := o.Sld.Cell.Shp
	sldS := sldH.S
	sldNp := len(o.Sld.IpsElem)
	sldNn := sldH.Nverts
	sldNu := o.Sld.Nu

	// shape functions of solid @ nodes of rod
	o.Nmat = la.MatAlloc(sldNn, rodNn)
	rodYn := make([]float64, o.Ndim)
	rodRn := make([]float64, 3)
	for m := 0; m < rodNn; m++ {
		for i := 0; i < o.Ndim; i++ {
			rodYn[i] = o.Rod.X[i][m]
		}
		err = sldH.InvMap(rodRn, rodYn, o.Sld.X)
		if err != nil {
			return
		}
		err = sldH.CalcAtR(o.Sld.X, rodRn, false)
		if err != nil {
			return
		}
		for n := 0; n < sldNn; n++ {
			o.Nmat[n][m] = sldH.S[n]
		}
	}

	// coulomb model => σc depends on p values of solid
	if o.Coulomb {

		// allocate variables
		o.Pmat = la.MatAlloc(sldNn, rodNp)
		o.Emat = la.MatAlloc(sldNn, sldNp)
		o.rodRp = la.MatAlloc(rodNp, 3)
		o.σNo = la.MatAlloc(sldNn, nsig)
		o.σIp = make([]float64, nsig)
		o.t1 = make([]float64, o.Ndim)
		o.t2 = make([]float64, o.Ndim)

		// fully consistent model
		if !o.Ncns {
			o.T1 = la.MatAlloc(rodNp, nsig)
			o.T2 = la.MatAlloc(rodNp, nsig)
			o.DσNoDu = utl.Deep4alloc(sldNn, nsig, sldNn, o.Ndim)
			o.DσDun = la.MatAlloc(nsig, o.Ndim)
		}

		// extrapolator matrix
		err = sldH.Extrapolator(o.Emat, o.Sld.IpsElem)
		if err != nil {
			return
		}

		// shape function of solid @ ips of rod
		for idx, ip := range o.Rod.IpsElem {
			rodYp := rodH.IpRealCoords(o.Rod.X, ip)
			err = sldH.InvMap(o.rodRp[idx], rodYp, o.Sld.X)
			if err != nil {
				return
			}
			err = sldH.CalcAtR(o.Sld.X, o.rodRp[idx], false)
			if err != nil {
				return
			}
			for n := 0; n < sldNn; n++ {
				o.Pmat[n][idx] = sldS[n]
			}
		}
	}

	// joint direction @ ip[idx]; corotational system aligned with rod element
	o.e0 = la.MatAlloc(rodNp, o.Ndim)
	o.e1 = la.MatAlloc(rodNp, o.Ndim)
	o.e2 = la.MatAlloc(rodNp, o.Ndim)
	π := make([]float64, o.Ndim) // Eq. (27)
	Q := la.MatAlloc(o.Ndim, o.Ndim)
	α := 666.0
	Jvec := rodH.Jvec3d[:o.Ndim]
	for idx, ip := range o.Rod.IpsElem {

		// auxiliary
		e0, e1, e2 := o.e0[idx], o.e1[idx], o.e2[idx]

		// interpolation functions and gradients
		err = rodH.CalcAtIp(o.Rod.X, ip, true)
		if err != nil {
			return
		}

		// compute basis vectors
		J := rodH.J
		π[0] = Jvec[0] + α
		π[1] = Jvec[1]
		e0[0] = Jvec[0] / J
		e0[1] = Jvec[1] / J
		if o.Ndim == 3 {
			π[2] = Jvec[2]
			e0[2] = Jvec[2] / J
		}
		la.MatSetDiag(Q, 1)
		la.VecOuterAdd(Q, -1, e0, e0) // Q := I - e0 dyad e0
		la.MatVecMul(e1, 1, Q, π)     // Eq. (29) * norm(E1)
		la.VecScale(e1, 0, 1.0/la.VecNorm(e1), e1)
		if o.Ndim == 3 {
			e2[0] = e0[1]*e1[2] - e0[2]*e1[1]
			e2[1] = e0[2]*e1[0] - e0[0]*e1[2]
			e2[2] = e0[0]*e1[1] - e0[1]*e1[0]
		}

		// compute auxiliary tensors
		if o.Coulomb {
			e1_dy_e1 := tsr.Alloc2()
			e2_dy_e2 := tsr.Alloc2()
			for i := 0; i < o.Ndim; i++ {
				for j := 0; j < o.Ndim; j++ {
					e1_dy_e1[i][j] = e1[i] * e1[j]
					e2_dy_e2[i][j] = e2[i] * e2[j]
				}
			}
			if !o.Ncns {
				tsr.Ten2Man(o.T1[idx], e1_dy_e1)
				tsr.Ten2Man(o.T2[idx], e2_dy_e2)
			}
		}
	}

	// auxiliary variables
	o.ΔuC = la.MatAlloc(rodNn, o.Ndim)
	o.Δw = make([]float64, o.Ndim)
	o.qb = make([]float64, o.Ndim)
	o.fC = make([]float64, rodNu)

	// temporary Jacobian matrices. see Eq. (57)
	o.Krr = la.MatAlloc(rodNu, rodNu)
	o.Krs = la.MatAlloc(rodNu, sldNu)
	o.Ksr = la.MatAlloc(sldNu, rodNu)
	o.Kss = la.MatAlloc(sldNu, sldNu)

	// debugging
	//if true {
	if false {
		o.debug_print_init()
	}

	// success
	return o.Ny * o.Ny, nil
}

// implementation ///////////////////////////////////////////////////////////////////////////////////

// SetEqs set equations
func (o *Rjoint) SetEqs(eqs [][]int, mixedform_eqs []int) (err error) {
	return
}

// SetEleConds set element conditions
func (o *Rjoint) SetEleConds(key string, f fun.Func, extra string) (err error) {
	return
}

// InterpStarVars interpolates star variables to integration points
func (o *Rjoint) InterpStarVars(sol *ele.Solution) (err error) {
	return
}

// adds -R to global residual vector fb
func (o *Rjoint) AddToRhs(fb []float64, sol *ele.Solution) (err error) {

	// auxiliary
	rodH := o.Rod.Cell.Shp
	rodS := rodH.S
	rodNn := rodH.Nverts
	sldH := o.Sld.Cell.Shp
	sldNn := sldH.Nverts
	h := o.Mdl.A_h

	// internal forces vector
	la.VecFill(o.fC, 0)

	// loop over rod's integration points
	var coef, τ, qn1, qn2 float64
	for idx, ip := range o.Rod.IpsElem {

		// auxiliary
		e0, e1, e2 := o.e0[idx], o.e1[idx], o.e2[idx]

		// interpolation functions and gradients
		err = rodH.CalcAtIp(o.Rod.X, ip, true)
		if err != nil {
			return
		}
		coef = ip[3] * rodH.J

		// state variables
		τ = o.States[idx].Sig
		qn1 = o.States[idx].Phi[0]
		qn2 = o.States[idx].Phi[1]

		// fC vector. Eq. (34)
		for i := 0; i < o.Ndim; i++ {
			o.qb[i] = τ*h*e0[i] + qn1*e1[i] + qn2*e2[i]
			for m := 0; m < rodNn; m++ {
				r := i + m*o.Ndim
				o.fC[r] += coef * rodS[m] * o.qb[i]
			}
		}
	}

	// fb = -Resid;  fR = -fC  and  fS = Nmat*fC  =>  fb := {fC, -Nmat*fC}
	for i := 0; i < o.Ndim; i++ {
		for m := 0; m < rodNn; m++ {
			r := i + m*o.Ndim
			I := o.Rod.Umap[r]
			fb[I] += o.fC[r] // fb := - (fR == -fC Eq (35))
			for n := 0; n < sldNn; n++ {
				s := i + n*o.Ndim
				J := o.Sld.Umap[s]
				fb[J] -= o.Nmat[n][m] * o.fC[r] // fb := - (fS Eq (36))
			}
		}
	}
	return
}

// adds element K to global Jacobian matrix Kb
func (o *Rjoint) AddToKb(Kb *la.Triplet, sol *ele.Solution, firstIt bool) (err error) {

	// auxiliary
	rodH := o.Rod.Cell.Shp
	rodS := rodH.S
	rodNn := rodH.Nverts
	sldH := o.Sld.Cell.Shp
	sldNn := sldH.Nverts
	h := o.Mdl.A_h
	kl := o.Mdl.A_kl

	// compute DσNoDu
	nsig := 2 * o.Ndim
	if o.Coulomb && !o.Ncns {

		// clear deep4 structure
		utl.Deep4set(o.DσNoDu, 0)

		// loop over solid's integration points
		for idx, ip := range o.Sld.IpsElem {

			// interpolation functions, gradients and variables @ ip
			err = sldH.CalcAtIp(o.Sld.X, ip, true)
			if err != nil {
				return
			}

			// consistent tangent model matrix
			err = o.Sld.MdlSmall.CalcD(o.Sld.D, o.Sld.States[idx], firstIt)
			if err != nil {
				return
			}

			// extrapolate derivatives
			for n := 0; n < sldNn; n++ {
				DerivSig(o.DσDun, n, o.Ndim, sldH.G, o.Sld.D)
				for m := 0; m < sldNn; m++ {
					for i := 0; i < nsig; i++ {
						for j := 0; j < o.Ndim; j++ {
							o.DσNoDu[m][i][n][j] += o.Emat[m][idx] * o.DσDun[i][j]
						}
					}
				}
			}
		}
	}

	// zero K matrices
	for i, _ := range o.Rod.Umap {
		for j, _ := range o.Rod.Umap {
			o.Krr[i][j] = 0
		}
		for j, _ := range o.Sld.Umap {
			o.Krs[i][j] = 0
			o.Ksr[j][i] = 0
		}
	}
	la.MatFill(o.Kss, 0)

	// auxiliary
	var coef float64
	var DτDω float64
	var Dwb0Du_nj, Dwb1Du_nj, Dwb2Du_nj float64
	var DτDu_nj, DqbDu_nij float64
	var Dwb0Dur_nj, Dwb1Dur_nj, Dwb2Dur_nj float64
	var DqbDur_nij float64

	// for consistent tanget operator
	var DτDσc, DσcDu_nj float64
	var Dp1Du_nj, Dp2Du_nj float64

	// loop over rod's integration points
	for idx, ip := range o.Rod.IpsElem {

		// auxiliary
		e0, e1, e2 := o.e0[idx], o.e1[idx], o.e2[idx]

		// interpolation functions and gradients
		err = rodH.CalcAtIp(o.Rod.X, ip, true)
		if err != nil {
			return
		}
		coef = ip[3] * rodH.J

		// model derivatives
		DτDω, DτDσc, err = o.Mdl.CalcD(o.States[idx], firstIt)
		if err != nil {
			return
		}

		// compute derivatives
		for j := 0; j < o.Ndim; j++ {

			// Krr and Ksr; derivatives with respect to ur_nj
			for n := 0; n < rodNn; n++ {

				// ∂wb/∂ur Eq (A.4)
				Dwb0Dur_nj = -rodS[n] * e0[j]
				Dwb1Dur_nj = -rodS[n] * e1[j]
				Dwb2Dur_nj = -rodS[n] * e2[j]

				// compute ∂■/∂ur derivatives
				c := j + n*o.Ndim
				for i := 0; i < o.Ndim; i++ {

					// ∂qb/∂ur Eq (A.2)
					DqbDur_nij = h*e0[i]*(DτDω*Dwb0Dur_nj) + kl*e1[i]*Dwb1Dur_nj + kl*e2[i]*Dwb2Dur_nj

					// Krr := ∂fr/∂ur Eq (58)
					for m := 0; m < rodNn; m++ {
						r := i + m*o.Ndim
						o.Krr[r][c] -= coef * rodS[m] * DqbDur_nij
					}

					//  Ksr := ∂fs/∂ur Eq (60)
					for m := 0; m < sldNn; m++ {
						r := i + m*o.Ndim
						for p := 0; p < rodNn; p++ {
							o.Ksr[r][c] += coef * o.Nmat[m][p] * rodS[p] * DqbDur_nij
						}
					}
				}
			}

			// Krs and Kss
			for n := 0; n < sldNn; n++ {

				// ∂σc/∂us_nj
				DσcDu_nj = 0
				if o.Coulomb && !o.Ncns {

					// Eqs (A.10) (A.11) and (A.12)
					Dp1Du_nj, Dp2Du_nj = 0, 0
					for m := 0; m < sldNn; m++ {
						for i := 0; i < nsig; i++ {
							Dp1Du_nj += o.Pmat[m][idx] * o.T1[idx][i] * o.DσNoDu[m][i][n][j]
							Dp2Du_nj += o.Pmat[m][idx] * o.T2[idx][i] * o.DσNoDu[m][i][n][j]
						}
					}
					DσcDu_nj = (Dp1Du_nj + Dp2Du_nj) / 2.0
				}

				// ∂wb/∂us Eq (A.5)
				Dwb0Du_nj, Dwb1Du_nj, Dwb2Du_nj = 0, 0, 0
				for m := 0; m < rodNn; m++ {
					Dwb0Du_nj += rodS[m] * o.Nmat[n][m] * e0[j]
					Dwb1Du_nj += rodS[m] * o.Nmat[n][m] * e1[j]
					Dwb2Du_nj += rodS[m] * o.Nmat[n][m] * e2[j]
				}

				// ∂τ/∂us_nj highlighted term in Eq (A.3)
				DτDu_nj = DτDω * Dwb0Du_nj
				if !o.Ncns {
					DτDu_nj += DτDσc * DσcDu_nj
				}

				// compute ∂■/∂us derivatives
				c := j + n*o.Ndim
				for i := 0; i < o.Ndim; i++ {

					// ∂qb/∂us Eq (A.3)
					DqbDu_nij = h*e0[i]*DτDu_nj + kl*e1[i]*Dwb1Du_nj + kl*e2[i]*Dwb2Du_nj

					// Krs := ∂fr/∂us Eq (59)
					for m := 0; m < rodNn; m++ {
						r := i + m*o.Ndim
						o.Krs[r][c] -= coef * rodS[m] * DqbDu_nij
					}

					// Kss := ∂fs/∂us Eq (61)
					for m := 0; m < sldNn; m++ {
						r := i + m*o.Ndim
						for p := 0; p < rodNn; p++ {
							o.Kss[r][c] += coef * o.Nmat[m][p] * rodS[p] * DqbDu_nij
						}
					}
				}
			}
		}
	}

	// debug
	//if true {
	if false {
		o.debug_print_K()
	}

	// add K to sparse matrix Kb
	for i, I := range o.Rod.Umap {
		for j, J := range o.Rod.Umap {
			Kb.Put(I, J, o.Krr[i][j])
		}
		for j, J := range o.Sld.Umap {
			Kb.Put(I, J, o.Krs[i][j])
			Kb.Put(J, I, o.Ksr[j][i])
		}
	}
	for i, I := range o.Sld.Umap {
		for j, J := range o.Sld.Umap {
			Kb.Put(I, J, o.Kss[i][j])
		}
	}
	return
}

// Update perform (tangent) update
func (o *Rjoint) Update(sol *ele.Solution) (err error) {

	// auxiliary
	nsig := 2 * o.Ndim
	rodH := o.Rod.Cell.Shp
	rodS := rodH.S
	rodNn := rodH.Nverts
	sldH := o.Sld.Cell.Shp
	sldNn := sldH.Nverts
	kl := o.Mdl.A_kl

	// extrapolate stresses at integration points of solid element to its nodes
	if o.Coulomb {
		la.MatFill(o.σNo, 0)
		for idx, _ := range o.Sld.IpsElem {
			σ := o.Sld.States[idx].Sig
			for i := 0; i < nsig; i++ {
				for m := 0; m < sldNn; m++ {
					o.σNo[m][i] += o.Emat[m][idx] * σ[i]
				}
			}
		}
	}

	// interpolate Δu of solid to find ΔuC @ rod node; Eq (30)
	var r, I int
	for m := 0; m < rodNn; m++ {
		for i := 0; i < o.Ndim; i++ {
			o.ΔuC[m][i] = 0
			for n := 0; n < sldNn; n++ {
				r = i + n*o.Ndim
				I = o.Sld.Umap[r]
				o.ΔuC[m][i] += o.Nmat[n][m] * sol.ΔY[I] // Eq (30)
			}
		}
	}

	// loop over ips of rod
	var Δwb0, Δwb1, Δwb2, σc float64
	for idx, ip := range o.Rod.IpsElem {

		// auxiliary
		e0, e1, e2 := o.e0[idx], o.e1[idx], o.e2[idx]

		// interpolation functions and gradients
		err = rodH.CalcAtIp(o.Rod.X, ip, true)
		if err != nil {
			return
		}

		// interpolated relative displacements @ ip of join; Eqs (31) and (32)
		for i := 0; i < o.Ndim; i++ {
			o.Δw[i] = 0
			for m := 0; m < rodNn; m++ {
				r = i + m*o.Ndim
				I = o.Rod.Umap[r]
				o.Δw[i] += rodS[m] * (o.ΔuC[m][i] - sol.ΔY[I]) // Eq (31) and (32)
			}
		}

		// relative displacements in the corotational system
		Δwb0, Δwb1, Δwb2 = 0, 0, 0
		for i := 0; i < o.Ndim; i++ {
			Δwb0 += e0[i] * o.Δw[i]
			Δwb1 += e1[i] * o.Δw[i]
			Δwb2 += e2[i] * o.Δw[i]
		}

		// new confining stress
		σc = 0.0
		if o.Coulomb {

			// calculate σIp
			for j := 0; j < nsig; j++ {
				o.σIp[j] = 0
				for n := 0; n < sldNn; n++ {
					o.σIp[j] += o.Pmat[n][idx] * o.σNo[n][j]
				}
			}

			// calculate t1 and t2
			for i := 0; i < o.Ndim; i++ {
				o.t1[i], o.t2[i] = 0, 0
				for j := 0; j < o.Ndim; j++ {
					o.t1[i] += tsr.M2T(o.σIp, i, j) * e1[j]
					o.t2[i] += tsr.M2T(o.σIp, i, j) * e2[j]
				}
			}

			// calculate p1, p2 and σcNew
			p1, p2 := 0.0, 0.0
			for i := 0; i < o.Ndim; i++ {
				p1 += o.t1[i] * e1[i]
				p2 += o.t2[i] * e2[i]
			}

			// σcNew
			σc = -(p1 + p2) / 2.0
		}

		// update model
		err = o.Mdl.Update(o.States[idx], σc, Δwb0)
		if err != nil {
			return
		}
		o.States[idx].Phi[0] += kl * Δwb1 // qn1
		o.States[idx].Phi[1] += kl * Δwb2 // qn2

		// debugging
		//if true {
		if false {
			o.debug_update(idx, Δwb0, Δwb1, Δwb2, σc)
		}
	}
	return
}

// internal variables ///////////////////////////////////////////////////////////////////////////////

// SetIniIvs sets initial ivs for given values in sol and ivs map
func (o *Rjoint) SetIniIvs(sol *ele.Solution, ivs map[string][]float64) (err error) {
	nip := len(o.Rod.IpsElem)
	o.States = make([]*solid.OnedState, nip)
	o.StatesBkp = make([]*solid.OnedState, nip)
	o.StatesAux = make([]*solid.OnedState, nip)
	for i := 0; i < nip; i++ {
		o.States[i], _ = o.Mdl.InitIntVars1D()
		o.StatesBkp[i] = o.States[i].GetCopy()
		o.StatesAux[i] = o.States[i].GetCopy()
	}
	return
}

// BackupIvs create copy of internal variables
func (o *Rjoint) BackupIvs(aux bool) (err error) {
	if aux {
		for i, s := range o.StatesAux {
			s.Set(o.States[i])
		}
		return
	}
	for i, s := range o.StatesBkp {
		s.Set(o.States[i])
	}
	return
}

// RestoreIvs restore internal variables from copies
func (o *Rjoint) RestoreIvs(aux bool) (err error) {
	if aux {
		for i, s := range o.States {
			s.Set(o.StatesAux[i])
		}
		return
	}
	for i, s := range o.States {
		s.Set(o.StatesBkp[i])
	}
	return
}

// Ureset fixes internal variables after u (displacements) have been zeroed
func (o *Rjoint) Ureset(sol *ele.Solution) (err error) {
	return
}

// writer ///////////////////////////////////////////////////////////////////////////////////////////

// Encode encodes internal variables
func (o *Rjoint) Encode(enc utl.Encoder) (err error) {
	return enc.Encode(o.States)
}

// Decode decodes internal variables
func (o *Rjoint) Decode(dec utl.Decoder) (err error) {
	err = dec.Decode(&o.States)
	if err != nil {
		return
	}
	return o.BackupIvs(false)
}

// OutIpCoords returns the coordinates of integration points
func (o *Rjoint) OutIpCoords() (C [][]float64) {
	return o.Rod.OutIpCoords()
}

// OutIpKeys returns the integration points' keys
func (o *Rjoint) OutIpKeys() []string {
	return []string{"tau", "ompb"}
}

// OutIpVals returns the integration points' values corresponding to keys
func (o *Rjoint) OutIpVals(M *ele.IpsMap, sol *ele.Solution) {
	nip := len(o.Rod.IpsElem)
	for idx, _ := range o.Rod.IpsElem {
		M.Set("tau", idx, nip, o.States[idx].Sig)
		M.Set("ompb", idx, nip, o.States[idx].Alp[0])
	}
}

// debugging ////////////////////////////////////////////////////////////////////////////////////////

func (o *Rjoint) debug_print_init() {
	sldNn := o.Sld.Cell.Shp.Nverts
	rodNn := o.Rod.Cell.Shp.Nverts
	rodNp := len(o.Rod.IpsElem)
	io.Pf("Nmat =\n")
	for i := 0; i < sldNn; i++ {
		for j := 0; j < rodNn; j++ {
			io.Pf("%g ", o.Nmat[i][j])
		}
		io.Pf("\n")
	}
	io.Pf("\nPmat =\n")
	for i := 0; i < sldNn; i++ {
		for j := 0; j < rodNp; j++ {
			io.Pf("%g ", o.Pmat[i][j])
		}
		io.Pf("\n")
	}
	io.Pf("\n")
	la.PrintMat("e0", o.e0, "%20.13f", false)
	io.Pf("\n")
	la.PrintMat("e1", o.e1, "%20.13f", false)
	io.Pf("\n")
	la.PrintMat("e2", o.e2, "%20.13f", false)
}

func (o *Rjoint) debug_print_K() {
	sldNn := o.Sld.Cell.Shp.Nverts
	rodNn := o.Rod.Cell.Shp.Nverts
	K := la.MatAlloc(o.Ny, o.Ny)
	start := o.Sld.Nu
	for i := 0; i < o.Ndim; i++ {
		for m := 0; m < sldNn; m++ {
			r := i + m*o.Ndim
			for j := 0; j < o.Ndim; j++ {
				for n := 0; n < sldNn; n++ {
					c := j + n*o.Ndim
					K[r][c] = o.Kss[r][c]
				}
				for n := 0; n < rodNn; n++ {
					c := j + n*o.Ndim
					K[r][start+c] = o.Ksr[r][c]
					K[start+c][r] = o.Krs[c][r]
				}
			}
		}
	}
	for i := 0; i < o.Ndim; i++ {
		for m := 0; m < rodNn; m++ {
			r := i + m*o.Ndim
			for j := 0; j < o.Ndim; j++ {
				for n := 0; n < rodNn; n++ {
					c := j + n*o.Ndim
					K[start+r][start+c] = o.Krr[r][c]
				}
			}
		}
	}
	la.PrintMat("K", K, "%20.10f", false)
}

func (o *Rjoint) debug_update(idx int, Δwb0, Δwb1, Δwb2, σc float64) {
	τ := o.States[idx].Sig
	qn1 := o.States[idx].Phi[0]
	qn2 := o.States[idx].Phi[1]
	la.PrintVec("Δw", o.Δw, "%13.10f", false)
	io.Pf("Δwb0=%13.10f Δwb1=%13.10f Δwb2=%13.10f\n", Δwb0, Δwb1, Δwb2)
	la.PrintVec("σIp", o.σIp, "%13.10f", false)
	io.Pf("σc=%13.10f t1=%13.10f t2=%13.10f\n", σc, o.t1, o.t2)
	io.Pf("τ=%13.10f qn1=%13.10f qn2=%13.10f\n", τ, qn1, qn2)
}
