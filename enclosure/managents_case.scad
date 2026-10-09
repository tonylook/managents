// managents — 45° desk enclosure for the LCDWiki 4.0" ESP32-32E display (E32R40T).
//
// Two printed parts, both support-free:
//   body  — a wedge whose front face is inclined 45°; printed standing on its base.
//   bezel — the frame around the screen; printed face down.
// The board drops into the face from the front and is clamped between the two
// with 4 x M3x10 screws into M3 heat-set inserts. USB-C exits on the right side.
//
// Render one part:   openscad -D 'part="body"' -o body.stl managents_case.scad
// Parts: "assembly" (preview), "section" (assembly cut in half), "body", "bezel",
// "fit_test" (a quick coupon to check hole positions against the real board
// before the full print).

include <lib/e32r40t.scad>

part = "assembly";

/* [Shape] */
face_angle = 45;    // screen inclination from the desk, degrees
lip_h = 8;          // height of the vertical front lip below the screen face
top_flat = 8;       // flat strip along the top edge
base_depth = 88;    // front-to-back footprint; deeper = harder to tip when touched
wall = 2.4;         // shell wall thickness
rim = 4;            // face margin around the board opening
fit = 0.4;          // clearance between board and printed parts

/* [Screen frame] */
glass_gap = 0.3;    // air gap above the touch panel: the bezel never presses it
bezel_top = 1.6;    // frame thickness over the glass border
window_margin = 0.4; // window grows beyond the touch view area by this much

/* [Fasteners] */
screw_d = 3.4;          // M3 clearance
screw_head_d = 6.2;     // counterbore for M3 socket or pan head
screw_head_h = 3.2;
insert_d = 4.0;         // hole for M3 x 5.7 heat-set inserts (use 2.8 for self-tapping)
insert_h = 6.5;
corner_block = 9;       // support block size under each board corner
corner_block_h = 7;

/* [Extras] */
speaker = true;         // grille and mount for a 28 mm speaker on the back face
speaker_d = 28;
foot_d = 10.5;          // recesses for self-adhesive rubber feet
foot_h = 1.0;

$fn = 48;
eps = 0.01;

// --- Derived dimensions -------------------------------------------------------

face_u = board_pcb[1] + 2 * (rim + fit); // face width (along the desk)
face_v = board_pcb[0] + 2 * (rim + fit); // face height (up the slope)
bezel_h = board_front_t + glass_gap + bezel_top;
board_origin = [rim + fit, rim + fit];   // board's lower-left corner on the face

c = cos(face_angle);
s = sin(face_angle);
profile = [
    [0, 0],
    [0, lip_h],
    [face_v * c, lip_h + face_v * s],
    [face_v * c + top_flat, lip_h + face_v * s],
    [base_depth, 0],
];
height = lip_h + face_v * s;

assert(base_depth > face_v * c + top_flat, "base_depth must reach past the top edge");

// --- Coordinate frames --------------------------------------------------------

// Face frame: u along the desk, v up the slope, n out of the screen; n = 0 is
// the face surface, where the PCB's front copper sits.
module on_face() {
    translate([0, 0, lip_h]) rotate([face_angle, 0, 0]) children();
}

// Board frame (see lib/e32r40t.scad) inside the face frame: landscape with the
// USB-C edge on the right, which is the firmware's default rotation.
module on_board() {
    translate([board_origin[0] + board_pcb[1], board_origin[1], 0]) rotate([0, 0, 90]) children();
}

// --- Body ---------------------------------------------------------------------

module wedge(inset = 0) {
    translate([inset, 0, 0])
        rotate([90, 0, 90])
            linear_extrude(face_u - 2 * inset)
                offset(delta = -inset) polygon(profile);
}

module board_opening() {
    on_face() on_board()
        translate([-fit, -fit, -wall - 1])
            linear_extrude(wall + 2)
                offset(r = fit) offset(r = board_corner_r) offset(delta = -board_corner_r) square(board_pcb);
}

// Blocks under the four board corners: the board rests on them and the
// screws bite into heat-set inserts pressed into them.
module corner_blocks() {
    intersection() {
        wedge();
        on_face() on_board()
            for (h = board_holes) {
                x0 = h[0] < board_pcb[0] / 2 ? -fit - 3 : board_pcb[0] - corner_block;
                y0 = h[1] < board_pcb[1] / 2 ? -fit - 3 : board_pcb[1] - corner_block;
                translate([x0, y0, -board_pcb_t - corner_block_h])
                    cube([corner_block + fit + 3, corner_block + fit + 3, corner_block_h]);
            }
    }
}

module insert_holes() {
    on_face() on_board()
        for (h = board_holes) translate([h[0], h[1], 0]) {
            translate([0, 0, -board_pcb_t - insert_h]) cylinder(d = insert_d, h = insert_h + eps);
            translate([0, 0, -board_pcb_t - corner_block_h - 1]) cylinder(d = 2.5, h = corner_block_h + 2);
        }
}

// The PCB sits flush with the face: clear its thickness above the blocks.
module board_pocket() {
    on_face() on_board()
        translate([-fit, -fit, -board_pcb_t])
            linear_extrude(board_pcb_t + 1)
                offset(r = fit) square(board_pcb);
}

module usb_opening() {
    width = 14;    // fits USB-C plug overmolds up to ~13 mm
    depth = 9;
    on_face() on_board()
        translate([board_usb_center_x, 2, -depth / 2])
            rotate([90, 0, 0])
                linear_extrude(rim + fit + wall + 4)
                    offset(r = 1.5) offset(delta = -1.5) square([width, depth], center = true);
}

// Back face: centre point and outward orientation, for the speaker.
back_a = profile[3];
back_b = profile[4];
back_angle = atan2(back_b[0] - back_a[0], back_a[1] - back_b[1]); // from vertical

module on_back_face() {
    translate([face_u / 2, (back_a[0] + back_b[0]) / 2, (back_a[1] + back_b[1]) / 2])
        rotate([-90 + back_angle, 0, 0]) children(); // local z = outward normal
}

module speaker_grille() {
    pitch = 3.2;
    on_back_face()
        for (i = [-4:4], j = [-4:4]) {
            x = i * pitch + (j % 2) * pitch / 2;
            y = j * pitch * 0.866;
            if (x * x + y * y < pow(speaker_d / 2 - 3, 2))
                translate([x, y, -wall - 1]) cylinder(d = 2, h = wall + 2, $fn = 12);
        }
}

module speaker_ring() {
    on_back_face()
        translate([0, 0, -wall - 3])
            difference() {
                cylinder(d = speaker_d + 0.6 + 3.2, h = 3 + eps);
                translate([0, 0, -eps]) cylinder(d = speaker_d + 0.6, h = 3 + 3 * eps);
            }
}

module feet_recesses() {
    for (x = [12, face_u - 12], y = [12, base_depth - 14])
        translate([x, y, -eps]) cylinder(d = foot_d, h = foot_h + eps);
}

module body() {
    difference() {
        union() {
            difference() {
                wedge();
                wedge(inset = wall);
                board_opening();
            }
            corner_blocks();
            if (speaker) intersection() { wedge(); speaker_ring(); }
        }
        board_pocket();
        insert_holes();
        usb_opening();
        if (speaker) speaker_grille();
        feet_recesses();
    }
}

// --- Bezel --------------------------------------------------------------------

module bezel_window() {
    r = board_touch_visible;
    grow = window_margin;
    // 45° chamfer on the outside improves the view at an angle.
    hull() {
        translate([r[0] - grow, r[1] - grow, -1]) cube([r[2] + 2 * grow, r[3] + 2 * grow, bezel_h - bezel_top + 1]);
        translate([r[0] - grow - bezel_top, r[1] - grow - bezel_top, bezel_h])
            cube([r[2] + 2 * (grow + bezel_top), r[3] + 2 * (grow + bezel_top), eps]);
    }
}

module bezel() {
    difference() {
        union() {
            difference() {
                cube([face_u, face_v, bezel_h]);
                // Room for the LCD + touch stack and the parts on the PCB's end strips.
                on_board() translate([-fit, -fit, -1])
                    linear_extrude(board_front_t + glass_gap + 1)
                        offset(r = fit) square(board_pcb);
            }
            // Bosses press the PCB down at its mounting pads.
            on_board() for (h = board_holes)
                translate([h[0], h[1], 0]) cylinder(d = board_hole_pad_d + 0.4, h = bezel_h - eps);
        }
        on_board() {
            bezel_window();
            for (h = board_holes) translate([h[0], h[1], 0]) {
                translate([0, 0, -1]) cylinder(d = screw_d, h = bezel_h + 2);
                translate([0, 0, bezel_h - screw_head_h]) cylinder(d = screw_head_d, h = screw_head_h + 1);
            }
        }
    }
}

// --- Fit test coupon -----------------------------------------------------------

// A 1.2 mm plate with pins at the hole positions and the view-area cut-out.
// Lay the real board on it: pins must enter the holes, the window must frame the image.
module fit_test() {
    plate = 1.2;
    difference() {
        translate([-3, -3, 0])
            linear_extrude(plate) square([board_pcb[0] + 6, board_pcb[1] + 6]);
        translate([board_touch_visible[0], board_touch_visible[1], -1])
            cube([board_touch_visible[2], board_touch_visible[3], plate + 2]);
    }
    for (h = board_holes) translate([h[0], h[1], 0]) cylinder(d = board_hole_d - 0.3, h = plate + 3);
    // USB-C edge marker.
    translate([board_usb_center_x - 4.5, -3, 0]) cube([9, 1.5, plate + 1.5]);
}

// --- Output -------------------------------------------------------------------

if (part == "body") {
    body();
} else if (part == "bezel") {
    // Face down on the bed.
    translate([0, face_v, bezel_h]) rotate([180, 0, 0]) bezel();
} else if (part == "fit_test") {
    fit_test();
} else if (part == "section") {
    // Assembly cut through the middle, to check clearances behind the board.
    intersection() {
        union() {
            color("whitesmoke") body();
            on_face() {
                color("dimgray") bezel();
                on_board() e32r40t_mock();
            }
        }
        translate([face_u / 2, -1, -1]) cube([face_u, base_depth + 2, height + bezel_h + 10]);
    }
} else {
    color("whitesmoke") body();
    on_face() {
        color("dimgray") bezel();
        on_board() e32r40t_mock();
    }
}

echo(str("managents case: ", face_u, " x ", base_depth, " x ", height, " mm (w x d x h)"));
