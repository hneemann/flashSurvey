let elementVisible = null
let aCallOnHide = null

function showPopUpById(id, callOnHide) {
    hidePopUp()
    setTimeout(function () {
        elementVisible = document.getElementById(id);
        if (elementVisible!=null) {
            elementVisible.style.visibility = "visible"
            aCallOnHide = callOnHide
        }
    })
}

document.addEventListener("click", (evt) => {
    if (elementVisible != null) {
        let targetEl = evt.target; // clicked element
        do {
            if (targetEl === elementVisible) {
                // This is a click inside, does nothing, just return.
                return;
            }
            // Go up the DOM
            targetEl = targetEl.parentNode;
        } while (targetEl);
        // This is a click outside.
        hidePopUp()
        evt.preventDefault()
    }
});

function hidePopUp() {
    if (elementVisible != null) {
        elementVisible.style.visibility = "hidden"
        elementVisible = null
        if (aCallOnHide != null) {
            aCallOnHide();
            aCallOnHide = null
        }
    }
}
